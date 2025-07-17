package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

const TIMEOUT_REQUEST = 2 * time.Second
const TIMEOUT_SIGTERM_SIGKILL = 1 * time.Second        // Maybe slightly longer?
const TIMEOUT_WAIT_AFTER_KILL = 500 * time.Millisecond // Shorter wait after kill

type ReadabilityClient struct {
	cmd        *exec.Cmd
	httpClient *http.Client
	mu         sync.Mutex

	udsPath string
	logger  *slog.Logger
}

func NewReadabilityClient(
	ctx context.Context,
	logger *slog.Logger,
	childLogger *log.Logger,
	tempDir string,
	serverBinaryPath string,
	uid string,
) (*ReadabilityClient, error) {
	if uid == "" {
		uid = uuid.New().String()
	}

	dirInfo, err := os.Stat(tempDir)
	if err != nil || !dirInfo.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", tempDir)
	}

	_, err = os.Stat(serverBinaryPath)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("%s readability binary does not exist", serverBinaryPath)
	}

	udsPath := filepath.Join(tempDir, fmt.Sprintf("readability-client-%s.sock", uid))
	os.Remove(udsPath)

	cmd := exec.Command(serverBinaryPath, "--uds", udsPath)
	if childLogger != nil {
		cmd.Stdout = childLogger.Writer()
		cmd.Stderr = childLogger.Writer()
	} else {
		logger.Warn("readability binary logs are suppressed")
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start readability server: %w", err)
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", udsPath)
		},
		MaxIdleConns:    1,
		MaxConnsPerHost: 1,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   TIMEOUT_REQUEST,
	}

	client := &ReadabilityClient{
		cmd:        cmd,
		httpClient: httpClient,
		mu:         sync.Mutex{},
		udsPath:    udsPath,
		logger:     logger,
	}

	if err := client.healthcheck(ctx); err != nil {
		ctxClose, cancelClose := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancelClose()
		_ = client.Close(ctxClose)
		return nil, fmt.Errorf("server failed health check: %w", err)
	}
	client.logger.Info("readability server healthcheck passed") // Add this line

	return client, nil
}

func (rc *ReadabilityClient) Close(ctx context.Context) error {
	rc.mu.Lock()
	if rc.cmd == nil || rc.cmd.Process == nil {
		rc.mu.Unlock()
		rc.logger.Debug("Close called on already closed or non-started client")
		return nil // Already closed or not started
	}

	localCmd := rc.cmd
	pid := localCmd.Process.Pid // Get PID for logging before potentially losing Process state
	rc.cmd = nil                // Mark as closed immediately
	rc.mu.Unlock()              // Unlock earlier, don't hold lock during process wait

	rc.logger.Info("Closing readability server process", "pid", pid, "uds", rc.udsPath)

	// Ensure socket removal happens even if process handling fails or times out
	defer func() {
		removeErr := os.Remove(rc.udsPath)
		if removeErr != nil && !os.IsNotExist(removeErr) {
			rc.logger.Error("Failed to remove UDS socket file", "path", rc.udsPath, "error", removeErr)
		} else {
			rc.logger.Debug("Removed UDS socket file", "path", rc.udsPath)
		}
	}()

	// --- Start Wait Goroutine ---
	// Start waiting in the background *before* signaling.
	// This avoids race conditions where the process might exit between Signal and Wait.
	waitDone := make(chan error, 1)
	go func() {
		// Wait releases process resources. It returns ExitError or nil.
		waitErr := localCmd.Wait()
		rc.logger.Debug("Process Wait() completed", "pid", pid, "error", waitErr)
		waitDone <- waitErr
	}()

	// --- Send SIGTERM ---
	rc.logger.Debug("Sending SIGTERM", "pid", pid)
	err := localCmd.Process.Signal(syscall.SIGTERM)

	// Check if process already exited before or immediately after SIGTERM
	if err != nil {
		if errors.Is(err, os.ErrProcessDone) || strings.Contains(err.Error(), "process already finished") {
			rc.logger.Debug("Process already finished before/during SIGTERM", "pid", pid)
			// Wait for the already running Wait() goroutine to complete
			select {
			case <-waitDone:
				rc.logger.Info("Readability server closed (already finished)", "pid", pid)
				return nil
			case <-time.After(TIMEOUT_WAIT_AFTER_KILL): // Don't wait forever for cleanup
				rc.logger.Warn("Timeout waiting for Wait() after finding process already finished", "pid", pid)
				return fmt.Errorf("process already finished but Wait() timed out")
			}
		}
		// Log other signal errors, but still proceed to wait/kill logic below
		rc.logger.Error("Failed to send SIGTERM, proceeding with wait/kill", "pid", pid, "error", err)
		// Note: Error during SIGTERM doesn't necessarily mean wait/kill won't work
	}

	// --- Wait for Graceful Exit or Context Timeout ---
	select {
	case waitErr := <-waitDone:
		// Process exited gracefully (or crashed) after SIGTERM was sent (or if SIGTERM failed but process died anyway)
		rc.logger.Info("Readability server closed gracefully", "pid", pid, "exitErr", waitErr)
		return nil // Successful shutdown (waitErr usually ignored in Close)

	case <-ctx.Done():
		// Context timed out, SIGTERM didn't work fast enough. Force Kill.
		rc.logger.Warn("Graceful shutdown timed out, sending SIGKILL", "pid", pid)
		killErr := localCmd.Process.Signal(syscall.SIGKILL)
		if killErr != nil && !errors.Is(killErr, os.ErrProcessDone) && !strings.Contains(killErr.Error(), "process already finished") {
			rc.logger.Error("Failed to send SIGKILL", "pid", pid, "error", killErr)
			// Even if SIGKILL fails, continue to wait briefly for the Wait() goroutine
		} else if killErr == nil {
			rc.logger.Debug("SIGKILL sent successfully", "pid", pid)
		} else {
			rc.logger.Debug("Process likely finished before SIGKILL could be sent", "pid", pid)
		}

		// Wait a short fixed duration for the Wait() goroutine to complete after SIGKILL
		select {
		case <-waitDone:
			rc.logger.Info("Readability server closed (killed)", "pid", pid)
			// Return context error because timeout initiated the kill
			return fmt.Errorf("readability server closed via kill after timeout: %w", ctx.Err())
		case <-time.After(TIMEOUT_WAIT_AFTER_KILL):
			rc.logger.Error("Process Wait() did not complete even after SIGKILL timeout", "pid", pid)
			// Return context error, but indicate Wait() also failed
			return fmt.Errorf("readability server timeout, SIGKILL sent but Wait() did not complete: %w", ctx.Err())
		}
	}
}

type ReadabilityResponseSuccess struct {
	Title string `json:"title"`
	// Byline        string    `json:"byline"`
	// Dir           *string   `json:"dir"`
	// Lang          string    `json:"lang"`
	TextContent string `json:"textContent"`
	Content     string `json:"content"`
	// Length        int       `json:"length"`
	Excerpt       string `json:"excerpt"`
	SiteName      string `json:"siteName"`
	PublishedTime string `json:"publishedTime"`
}

type ReadabilityResponseError struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
}

func (rc *ReadabilityClient) Parse(ctx context.Context, htmlBody string, url string) (*ReadabilityResponseSuccess, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if rc.cmd == nil {
		return nil, fmt.Errorf("readability client is closed or server process exited")
	}

	reqURL := "http://localhost/" // Dummy URL for UDS
	req, err := http.NewRequestWithContext(ctx, "POST", reqURL, strings.NewReader(htmlBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "text/html; charset=utf-8")
	req.Header.Set("X-Document-URL", url)

	start := time.Now()
	resp, err := rc.httpClient.Do(req)
	duration := time.Since(start)
	rc.logger.Debug("request duration", "duration", duration)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("request cancelled or timed out: %w", ctx.Err())
		}
		return nil, fmt.Errorf("failed to send request to readability server: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body (status %d): %w", resp.StatusCode, err)
	}

	if resp.StatusCode == http.StatusOK {
		var successResp ReadabilityResponseSuccess
		if err := json.Unmarshal(bodyBytes, &successResp); err != nil {
			return nil, fmt.Errorf("failed to parse successful response JSON (status %d): %w", resp.StatusCode, err)
		}
		rc.logger.Info("successfully parsed document",
			"title", successResp.Title,
			"siteName", successResp.SiteName)
		return &successResp, nil
	} else {
		var errorResp ReadabilityResponseError
		if err := json.Unmarshal(bodyBytes, &errorResp); err == nil && errorResp.Error != "" {
			details := ""
			if errorResp.Details != "" {
				details = fmt.Sprintf(" (%s)", errorResp.Details)
			}
			return nil, fmt.Errorf("server returned status %d: %s%s", resp.StatusCode, errorResp.Error, details)
		}
		// If JSON parsing fails or doesn't yield a useful error message, return generic error
		errMsg := strings.TrimSpace(string(bodyBytes))
		if len(errMsg) > 200 {
			errMsg = errMsg[:200] + "..."
		}
		if errMsg == "" {
			errMsg = http.StatusText(resp.StatusCode)
		}
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, errMsg)
	}
}

func (rc *ReadabilityClient) healthcheck(ctx context.Context) error {
	const retryDelay = 200 * time.Millisecond
	const attemptTimeout = 100 * time.Millisecond
	const dummyHTML = "<html><body>health check</body></html>"
	const dummyURL = "http://health.check/local"

	startTime := time.Now()

	var lastErr error
	ticker := time.NewTicker(retryDelay)
	defer ticker.Stop()

	for {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, attemptTimeout)
		_, parseErr := rc.Parse(attemptCtx, dummyHTML, dummyURL)
		attemptCancel()

		if parseErr == nil {
			duration := time.Since(startTime)
			rc.logger.Info("Healthcheck passed", "duration", duration)
			return nil
		}

		lastErr = parseErr

		select {
		case <-ctx.Done():
			contextErr := ctx.Err()
			totalDuration := time.Since(startTime)
			rc.logger.Error("Healthcheck failed: context ended", "duration", totalDuration, "lastError", lastErr, "contextError", contextErr)
			return fmt.Errorf("healthcheck failed after %v: context %v (last error: %w)", totalDuration, contextErr, lastErr)
		case <-ticker.C:
			continue
		}
	}
}

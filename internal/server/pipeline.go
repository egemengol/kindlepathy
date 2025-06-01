package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/egemengol/ereader/internal/readability"
)

func ProcessURL(ctx context.Context, url string, client *http.Client, readability *readability.ReadabilityClient, db *sql.DB, logger *slog.Logger) error {
	logger.Info("fetching page content", "url", url)
	reqCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch page: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch page, status code: %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/html") {
		return fmt.Errorf("unexpected content type: %s. Expected text/html", contentType)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}
	html := string(bodyBytes)

	// Step 2: Process with readability
	logger.Info("processing with readability", "url", url)
	readable, err := readability.Parse(ctx, html, url)
	if err != nil {
		return fmt.Errorf("failed to process with readability: %w", err)
	}

	// Convert readable content to JSON
	processedJSON, err := json.Marshal(readable)
	if err != nil {
		return fmt.Errorf("failed to marshal processed content: %w", err)
	}

	// Step 3: Store processed content in database
	_, err = db.ExecContext(ctx, `
	    INSERT INTO page_cache (url, readability_output)
	    VALUES (?, ?)
	    ON CONFLICT(url) DO UPDATE SET
	    readability_output = excluded.readability_output,
	    accessed_at = CURRENT_TIMESTAMP`, url, string(processedJSON))

	if err != nil {
		return fmt.Errorf("failed to store processed content: %w", err)
	}

	return nil
}

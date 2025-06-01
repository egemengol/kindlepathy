package readability

import (
	"context"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadabilityClient_Parse(t *testing.T) {
	// Setup context and logger
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	childLogger := log.New(os.Stdout, "readability-server: ", log.LstdFlags)

	// Get project root directory (one level up from internal)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current working directory: %v", err)
	}
	projectRoot := filepath.Dir(cwd) // Go up one directory from internal

	// Configure paths
	tempDir := os.TempDir()
	serverBinaryPath := filepath.Join(projectRoot, "readability", "readability")
	htmlFilePath := filepath.Join(projectRoot, "files", "pvsnp.html")

	// Create client
	client, err := NewReadabilityClient(
		ctx,
		logger,
		childLogger,
		tempDir,
		serverBinaryPath,
		"test-client",
	)
	if err != nil {
		t.Fatalf("Failed to create readability client: %v", err)
	}
	defer func() {
		if err := client.Close(ctx); err != nil {
			t.Errorf("Failed to close client: %v", err)
		}
	}()

	// Read HTML file
	htmlContent, err := os.ReadFile(htmlFilePath)
	if err != nil {
		t.Fatalf("Failed to read HTML file: %v", err)
	}

	// Test parsing
	start := time.Now()
	result, err := client.Parse(ctx, string(htmlContent), "https://example.com/test")
	duration := time.Since(start)
	t.Logf("Parse duration: %v", duration)
	if err != nil {
		t.Fatalf("Failed to parse HTML: %v", err)
	}

	// Basic validation of results
	if result.Title == "" {
		t.Error("Expected non-empty title")
	}
	if result.TextContent == "" {
		t.Error("Expected non-empty text content")
	}
	if result.Content == "" {
		t.Error("Expected non-empty content")
	}
	// fmt.Println(result.Content)
}

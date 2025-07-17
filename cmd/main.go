package main

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/dgraph-io/badger/v4"
	_ "github.com/mattn/go-sqlite3"

	"github.com/egemengol/kindlepathy/internal/core"
	migrate "github.com/egemengol/kindlepathy/internal/db"
	db "github.com/egemengol/kindlepathy/internal/db/generated"
	"github.com/egemengol/kindlepathy/internal/server"
)

func main() {
	ctx := context.Background()

	readabilityPath := os.Getenv("READABILITY_PATH")
	dbPath := os.Getenv("DB_PATH")
	cachePath := os.Getenv("CACHE_PATH")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	portInt := 0
	_, err := fmt.Sscanf(port, "%d", &portInt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid port number: %s\n", port)
		os.Exit(1)
	}
	sessionStoreSecret := []byte(os.Getenv("SESSION_SECRET"))
	if len(sessionStoreSecret) == 0 {
		// Use a default secret for development - DO NOT use in production
		sessionStoreSecret = []byte("dev-secret-key-32-bytes-long!!!")
		fmt.Fprintf(os.Stderr, "Warning: SESSION_SECRET not set, using default (development only)\n")
	}
	if len(sessionStoreSecret) < 32 {
		fmt.Fprintf(os.Stderr, "SESSION_SECRET must be at least 32 bytes long\n")
		os.Exit(1)
	}

	config := &Config{
		ReadabilityPath:    readabilityPath,
		DBPath:             dbPath,
		Port:               portInt,
		CachePath:          cachePath,
		SessionStoreSecret: sessionStoreSecret,
	}

	if err := run(ctx, os.Stdout, config); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

type Config struct {
	ReadabilityPath    string
	DBPath             string
	Port               int
	CachePath          string
	SessionStoreSecret []byte
}

func run(ctx context.Context, w io.Writer, config *Config) error {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelDebug.Level(),
	}))
	loggerReadability := log.Default()

	// TODO WAL and foreign keys
	sqlDB, err := sql.Open("sqlite3", config.DBPath)
	if err != nil {
		return err
	}
	err = migrate.Migrate(ctx, sqlDB)
	queries := db.New(sqlDB)

	logger.Info("Initializing Readability service...")
	readability, err := core.NewReadabilityClient(ctx, logger, loggerReadability, os.TempDir(), config.ReadabilityPath, "readability")
	if err != nil {
		log.Fatal(err)
	}

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	var cache *badger.DB
	if config.CachePath != "" {
		cache, err = badger.Open(badger.DefaultOptions(config.CachePath))
	}

	coreSingleton := core.NewCore(
		httpClient, readability, queries, logger, cache,
	)

	srv := server.NewServer(coreSingleton, logger, queries, config.SessionStoreSecret)

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", config.Port),
		Handler: srv,
	}

	errChan := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- fmt.Errorf("server failed: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("Received shutdown signal, initiating graceful shutdown...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		logger.Info("Shutting down HTTP server...")
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("HTTP server graceful shutdown failed", "error", err)
		}

		logger.Info("Closing Readability client...")
		readability.Close(shutdownCtx)

		if cache != nil {
			logger.Info("Closing cache...")
			if err := cache.Close(); err != nil {
				logger.Error("Failed to close cache", "error", err)
			}
		}

		logger.Info("Closing database connection...")
		if err := sqlDB.Close(); err != nil {
			logger.Error("Failed to close database", "error", err)
		}

		logger.Info("Shutdown complete.")
		return nil
	case err := <-errChan:
		return err
	}
}

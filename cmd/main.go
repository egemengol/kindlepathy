package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "modernc.org/sqlite"

	"github.com/egemengol/ereader/internal/readability"
	"github.com/egemengol/ereader/internal/server"
)

func main() {
	ctx := context.Background()

	readabilityPath := os.Getenv("READABILITY_PATH")
	sessionSecret := []byte(os.Getenv("SESSION_SECRET"))
	dbPath := os.Getenv("DB_PATH")
	port := os.Getenv("PORT")
	if port == "" { // Default port if not set
		port = "8080"
	}
	// Parse port as an integer
	portInt := 0
	_, err := fmt.Sscanf(port, "%d", &portInt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid port number: %s\n", port)
		os.Exit(1)
	}

	config := &Config{
		ReadabilityPath: readabilityPath,
		SessionSecret:   sessionSecret,
		DBPath:          dbPath,
		Port:            portInt, // Use the port variable
	}

	if err := run(ctx, os.Stdout, config); err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

type Config struct {
	ReadabilityPath string
	SessionSecret   []byte
	DBPath          string
	Port            int
}

func run(ctx context.Context, w io.Writer, config *Config) error {
	// Set up graceful shutdown
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Initialize logger
	logger := slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelDebug.Level(),
	}))
	loggerReadability := log.Default()

	// Run database migrations
	logger.Info("Running database migrations...", "dbPath", config.DBPath)
	m, err := migrate.New(
		"file://migrations",
		fmt.Sprintf("sqlite://%s", config.DBPath),
	)
	if err != nil {
		return fmt.Errorf("failed to create migration instance: %w", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("failed to run migrations: %w", err)
	}
	logger.Info("Database migrations completed successfully")

	// Initialize Readability service
	logger.Info("Initializing Readability service...")
	readability, err := readability.NewReadabilityClient(ctx, logger, loggerReadability, os.TempDir(), config.ReadabilityPath, "readability")
	if err != nil {
		log.Fatal(err)
	}

	// Initialize database connection
	logger.Info("Connecting to database...", "path", config.DBPath)
	db, err := sql.Open("sqlite", config.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	db.SetMaxOpenConns(1)

	// Test database connection
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}
	logger.Info("Database connection established successfully")

	httpClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Create the server
	srv := server.NewServer(logger, readability, db, httpClient, config.SessionSecret)

	// Serve with TLS
	// certFile := "localhost+2.pem"
	// keyFile := "localhost+2-key.pem"

	// Start server in goroutine
	errChan := make(chan error, 1)
	go func() {
		// logger.Info("Starting HTTPS server on :8080")
		// if err := http.ListenAndServeTLS(":8080", certFile, keyFile, srv); err != nil {
		logger.Info("Starting HTTP server on", "port", config.Port)
		if err := http.ListenAndServe(fmt.Sprintf(":%d", config.Port), srv); err != nil {
			errChan <- fmt.Errorf("server failed: %w", err)
		}
	}()

	// Wait for shutdown signal or error
	select {
	case <-ctx.Done():
		logger.Info("Received shutdown signal, stopping server...")
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
		defer cancel()
		readability.Close(closeCtx)
		return nil
	case err := <-errChan:
		return err
	}
}

func runMigrations(dbPath string) error {
	m, err := migrate.New(
		"file://migrations",
		fmt.Sprintf("sqlite://%s", dbPath),
	)
	if err != nil {
		return fmt.Errorf("could not create database driver: %w", err)
	}

	// Run migrations
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("could not run migrations: %w", err)
	}

	return nil
}

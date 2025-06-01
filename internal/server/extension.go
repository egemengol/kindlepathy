package server

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/egemengol/ereader/internal/readability"
	"github.com/gorilla/sessions"
)

// extension.go contains endpoints and middleware specific to the extension client

// handleExtensionCheckAuth is a CORS-enabled endpoint to check authentication status
func handleExtensionCheckAuth(logger *slog.Logger, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := sessionStore.Get(r, "read-elsewhere")
		if err != nil {
			logger.Error("Error getting session", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Check if the user is authenticated
		auth, ok := session.Values["authenticated"].(bool)
		if !ok || !auth {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
	})
}

type ExtensionArticle struct {
	Article readability.ReadabilityResponseSuccess `json:"article"`
	URL     string                                 `json:"url"`
}

// handleExtensionPostContent handles cleaned content submission from the extension
func handleExtensionPostContent(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get user ID from session
		userId, _, err := getUserId(r, db, sessionStore)
		if err != nil {
			logger.Error("Error getting user ID", "error", err)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Parse request body
		// body, err := io.ReadAll(r.Body)
		// if err != nil {
		// 	logger.Error("Error reading request body", "error", err)
		// 	http.Error(w, "Invalid request body", http.StatusBadRequest)
		// 	return
		// }
		// logger.Info("Request body", "body", string(body))

		var content ExtensionArticle
		if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
			// if err := json.Unmarshal(body, &content); err != nil {
			logger.Error("Error decoding request body", "error", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Start a transaction
		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			logger.Error("Error starting transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// Insert or update the page in the pages table
		var pageId int
		err = tx.QueryRowContext(r.Context(), `
            INSERT INTO pages (user_id, url)
            VALUES (?, ?)
            ON CONFLICT(user_id, url) DO UPDATE SET
            accessed_at = CURRENT_TIMESTAMP
            RETURNING id`, userId, content.URL).Scan(&pageId)
		if err != nil {
			logger.Error("Error inserting or updating page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Set this page as the active page for the user
		_, err = tx.ExecContext(r.Context(), `
            INSERT INTO user_active_page (user_id, page_id)
            VALUES (?, ?)
            ON CONFLICT(user_id) DO UPDATE SET page_id = excluded.page_id`,
			userId, pageId)
		if err != nil {
			logger.Error("Error updating active page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Convert content to JSON for storage in page_cache
		contentJSON, err := json.Marshal(content.Article)
		if err != nil {
			logger.Error("Error marshaling content", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Insert or update the page_cache table
		_, err = tx.ExecContext(r.Context(), `
            INSERT INTO page_cache (url, readability_output)
            VALUES (?, ?)
            ON CONFLICT(url) DO UPDATE SET
            readability_output = excluded.readability_output,
            accessed_at = CURRENT_TIMESTAMP`, content.URL, string(contentJSON))
		if err != nil {
			logger.Error("Error storing content", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Commit the transaction
		if err = tx.Commit(); err != nil {
			logger.Error("Error committing transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
	})
}

// newCORSMiddleware creates a middleware that adds CORS headers to responses
func newExtensionCORSMiddleware(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Log the request method and path
			// logger.Info("CORS middleware invoked", "method", r.Method, "path", r.URL.Path)

			// Get the origin from the request
			origin := r.Header.Get("Origin")

			// Set CORS headers
			w.Header().Set("Access-Control-Allow-Origin", origin) // Allow the specific origin
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Credentials", "true") // Allow credentials

			// Handle preflight requests
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// Pass the request to the next handler
			next.ServeHTTP(w, r)
		})
	}
}

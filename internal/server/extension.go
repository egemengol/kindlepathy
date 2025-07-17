package server

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/egemengol/kindlepathy/internal/core"
	"github.com/gorilla/sessions"
)

// extension.go contains endpoints and middleware specific to the extension client

// handleExtensionCheckAuth is a CORS-enabled endpoint to check authentication status
func handleExtensionCheckAuth(logger *slog.Logger, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := sessionStore.Get(r, "kindlepathy")
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
	Article struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	} `json:"article"`
	URL string `json:"url"`
}

// handleExtensionPostContent handles cleaned content submission from the extension
func handleExtensionPostContent(logger *slog.Logger, c *core.Core, auth *AuthService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get user from context (populated by auth middleware)
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		// Parse request body
		var content ExtensionArticle
		if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
			logger.Error("Error decoding request body", "error", err)
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		// Add item with uploaded content
		_, err = c.AddItemWithUploadedContent(r.Context(), authedUser.ID, content.Article.Title, content.URL, content.Article.Content, time.Now())
		if err != nil {
			logger.Error("Error adding item with uploaded content", "error", err)
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

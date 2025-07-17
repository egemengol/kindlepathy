package server

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egemengol/kindlepathy/internal/core"
	db "github.com/egemengol/kindlepathy/internal/db/generated"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

//go:embed read.html
var TEMPLATE_READ string

func NewServer(core *core.Core, logger *slog.Logger, queries *db.Queries, sessionStoreSecret []byte) http.Handler {
	sessionStore := sessions.NewCookieStore(sessionStoreSecret)
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
	}

	mux := http.NewServeMux()

	addRoutes(mux, core, logger, queries, sessionStore)

	return mux
}

func addRoutes(mux *http.ServeMux, c *core.Core, logger *slog.Logger, queries *db.Queries, sessionStore *sessions.CookieStore) {
	fs := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	auth := NewAuthService(queries, sessionStore)

	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "login.html"))
	})
	mux.Handle("POST /login", handleLoginPost(logger, queries, sessionStore))

	mux.HandleFunc("GET /signup", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "signup.html"))
	})
	mux.Handle("POST /signup", handleSignupPost(logger, queries))
	mux.Handle("/logout", handleLogout(sessionStore))

	mux.HandleFunc("/privacy", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "privacy.html"))
	})

	authMiddleware := newAuthMiddleware(sessionStore, queries)

	mux.Handle("DELETE /library/{id}", authMiddleware(handleLibraryItemDelete(c, auth, logger)))
	mux.Handle("PATCH /library/{id}", authMiddleware(handleLibraryItemPatch(auth, logger)))
	mux.Handle("GET /library", authMiddleware(handleLibraryGet(c, auth, logger)))
	mux.Handle("POST /library", authMiddleware(handleLibraryPost(c, auth, logger)))

	corsMiddleware := newExtensionCORSMiddleware(logger)
	mux.Handle("GET /ext/check-auth", corsMiddleware(handleExtensionCheckAuth(logger, sessionStore)))
	mux.Handle("POST /ext/article", corsMiddleware(authMiddleware(handleExtensionPostContent(logger, c, auth))))

	/////////////

	mux.Handle("GET /read/{id}", authMiddleware(handleRead(c, auth, logger)))
	mux.Handle("GET /read", authMiddleware(handleReadActive(c, auth, logger)))
	mux.Handle("POST /read/{id}", authMiddleware(handleReadNav(c, auth, logger)))
	mux.Handle("POST /read", authMiddleware(handleReadNavActive(c, auth, logger)))
}

func handleReadActive(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	tmpl := template.Must(template.New("read").Parse(TEMPLATE_READ))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		if authedUser.ActiveItemID == nil {
			http.Error(w, "No active item", http.StatusNotFound)
			return
		}

		activeItemID := *authedUser.ActiveItemID

		// Check ownership
		if err := auth.RequireOwnership(r.Context(), authedUser.Username, activeItemID); err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		itemScs, err := c.ReadItem(r.Context(), activeItemID, time.Now())
		if err != nil {
			logger.Error("Error reading item", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		data := struct {
			Title   string
			Content template.HTML
			NavNext string
			NavPrev string
			ItemID  int64
		}{
			Title:   itemScs.Title,
			Content: template.HTML(itemScs.ContentHTML),
			NavNext: core.RelativizeURL(itemScs.NavNext),
			NavPrev: core.RelativizeURL(itemScs.NavPrev),
			ItemID:  activeItemID,
		}

		if err := tmpl.Execute(w, data); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

func handleRead(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	tmpl := template.Must(template.New("read").Parse(TEMPLATE_READ))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		itemID := r.PathValue("id")
		itemIDInt, err := strconv.ParseInt(itemID, 10, 64)
		if err != nil {
			logger.Error("Error converting ID to int", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		if err := auth.RequireOwnership(r.Context(), authedUser.Username, itemIDInt); err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		itemScs, err := c.ReadItem(r.Context(), itemIDInt, time.Now())
		if err != nil {
			logger.Error("Error reading item", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		data := struct {
			Title   string
			Content template.HTML
			NavNext string
			NavPrev string
			ItemID  int64
		}{
			Title:   itemScs.Title,
			Content: template.HTML(itemScs.ContentHTML),
			NavNext: core.RelativizeURL(itemScs.NavNext),
			NavPrev: core.RelativizeURL(itemScs.NavPrev),
			ItemID:  itemIDInt,
		}

		if err := tmpl.Execute(w, data); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

func navigateItemShared(ctx context.Context, c *core.Core, queries *db.Queries, itemID int64, targetPath string) error {
	if targetPath != "" && (len(targetPath) == 0 || targetPath[0] != '/') {
		return fmt.Errorf("invalid target path: %s", targetPath)
	}

	c.NavigateItem(ctx, itemID, targetPath)
	return nil
}

func handleReadNavActive(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		if err := r.ParseForm(); err != nil {
			logger.Error("Error parsing form", "error", err)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		itemIDStr := r.FormValue("item_id")
		itemID, err := strconv.ParseInt(itemIDStr, 10, 64)
		if err != nil {
			logger.Error("Error converting item ID to int", "error", err)
			http.Error(w, "Invalid item ID", http.StatusBadRequest)
			return
		}

		// Check ownership
		if err := auth.RequireOwnership(r.Context(), authedUser.Username, itemID); err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		// Set active item
		err = auth.queries.UsersSetActiveItem(r.Context(), db.UsersSetActiveItemParams{
			ActiveItemID: itemID,
			ID:           authedUser.ID,
		})
		if err != nil {
			logger.Error("Error setting active item", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		targetPath := r.FormValue("target")
		if err := navigateItemShared(r.Context(), c, auth.queries, itemID, targetPath); err != nil {
			logger.Error("Error navigating item", "error", err)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/read", http.StatusSeeOther)
	})
}

func handleReadNav(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		itemID := r.PathValue("id")
		itemIDInt, err := strconv.ParseInt(itemID, 10, 64)
		if err != nil {
			logger.Error("Error converting item ID to int", "error", err)
			http.Error(w, "Invalid item ID", http.StatusBadRequest)
			return
		}

		if err := auth.RequireOwnership(r.Context(), authedUser.Username, itemIDInt); err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		if err := r.ParseForm(); err != nil {
			logger.Error("Error parsing form", "error", err)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}
		targetPath := r.FormValue("target")
		if err := navigateItemShared(r.Context(), c, auth.queries, itemIDInt, targetPath); err != nil {
			logger.Error("Error navigating item", "error", err)
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		http.Redirect(w, r, "/read/"+itemID, http.StatusSeeOther)
	})
}

func handleLoginPost(logger *slog.Logger, queries *db.Queries, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			username := r.FormValue("username")
			providedPassword := r.FormValue("password")

			hashedPassword, err := queries.UsersGetPassword(r.Context(), username)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					http.Error(w, "Invalid credentials", http.StatusUnauthorized)
					return
				}
				logger.Error("Failed to get password", "username", username, "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
			err = bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(providedPassword))
			if err != nil {
				http.Error(w, "Invalid credentials", http.StatusUnauthorized)
				return
			}

			session, err := sessionStore.Get(r, "kindlepathy")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			session.Values["authenticated"] = true
			session.Values["username"] = username
			session.Save(r, w)

			http.Redirect(w, r, "/library", http.StatusSeeOther)
		},
	)
}

func handleSignupPost(logger *slog.Logger, queries *db.Queries) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			username := r.FormValue("username")
			password := r.FormValue("password")
			confirmPassword := r.FormValue("confirm_password")

			if username == "" || password == "" {
				http.Error(w, "Username and password are required", http.StatusBadRequest)
				return
			}

			if password != confirmPassword {
				http.Error(w, "Passwords do not match", http.StatusBadRequest)
				return
			}

			_, err := queries.UsersGetByName(r.Context(), username)
			if err == nil {
				http.Error(w, "Username already exists", http.StatusConflict)
				return
			}
			if err != sql.ErrNoRows {
				logger.Error("Database error checking username", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				logger.Error("Error hashing password", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			_, err = queries.UsersAdd(r.Context(), db.UsersAddParams{Username: username, Password: string(hashedPassword)})
			if err != nil {
				logger.Error("Error creating user", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, "/login", http.StatusSeeOther)
		},
	)
}

func handleLogout(sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := sessionStore.Get(r, "kindlepathy")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Clear session values
		session.Values["authenticated"] = false
		session.Values["username"] = ""

		// Save the session
		err = session.Save(r, w)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Redirect to login page
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	})
}

func newAuthMiddleware(sessionStore *sessions.CookieStore, queries *db.Queries) func(h http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := sessionStore.Get(r, "kindlepathy")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			username, ok := session.Values["username"].(string)
			if !ok {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			user, err := queries.UsersGetByName(r.Context(), username)
			if err != nil {
				// If user in session doesn't exist in DB, treat as logged out.
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			var activeItemID *int64
			if user.ActiveItemID != nil {
				if id, ok := user.ActiveItemID.(int64); ok {
					activeItemID = &id
				}
			}

			authedUser := AuthenticatedUser{
				ID:           user.ID,
				Username:     user.Username,
				ActiveItemID: activeItemID,
			}

			ctx := context.WithValue(r.Context(), userContextKey, authedUser)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

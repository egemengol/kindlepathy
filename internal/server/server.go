package server

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/egemengol/ereader/internal/readability"
	"github.com/gorilla/sessions"
	"golang.org/x/crypto/bcrypt"
)

func NewServer(logger *slog.Logger, readability *readability.ReadabilityClient, db *sql.DB, httpClient *http.Client, sessionStoreSecret []byte) http.Handler {
	sessionStore := sessions.NewCookieStore(sessionStoreSecret)
	sessionStore.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
	}

	mux := http.NewServeMux()

	fs := http.FileServer(http.Dir("web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	addRoutes(mux, logger, readability, db, httpClient, sessionStore)

	return mux
}

func checkPassword(providedPassword, hashedPassword string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(providedPassword))
	return err == nil
}

func handleLoginPost(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			username := r.FormValue("username")
			password := r.FormValue("password")

			var hashedPassword string
			err := db.QueryRow("SELECT password FROM users WHERE username = ?", username).Scan(&hashedPassword)
			if err != nil || !checkPassword(password, hashedPassword) {
				http.Error(w, "Invalid credentials", http.StatusUnauthorized)
				return
			}

			session, err := sessionStore.Get(r, "read-elsewhere")
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

func handleSignupPost(logger *slog.Logger, db *sql.DB) http.Handler {
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

			var exists bool
			err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM users WHERE username = ?)", username).Scan(&exists)
			if err != nil {
				logger.Error("Database error checking username", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			if exists {
				http.Error(w, "Username already exists", http.StatusConflict)
				return
			}

			hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				logger.Error("Error hashing password", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			_, err = db.Exec("INSERT INTO users (username, password) VALUES (?, ?)", username, hashedPassword)
			if err != nil {
				logger.Error("Error creating user", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}

			http.Redirect(w, r, "/login", http.StatusSeeOther)
		},
	)
}

func handleRead(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	tmpl := template.Must(template.ParseFiles(filepath.Join("web", "read.html")))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate") // Prevent caching
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		session, err := sessionStore.Get(r, "read-elsewhere")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		username, ok := session.Values["username"].(string)
		if !ok {
			http.Error(w, "User not found in session", http.StatusInternalServerError)
			return
		}

		// Get user's active URL
		var userId int
		err = db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&userId)
		if err != nil {
			logger.Error("Error getting user ID", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		var activeURL string
		err = db.QueryRow(`
		    SELECT p.url
		    FROM user_active_page uap
		    JOIN pages p ON p.id = uap.page_id
		    WHERE uap.user_id = ?`, userId).Scan(&activeURL)
		if err != nil {
			logger.Error("Error getting active URL", "error", err)
			http.Error(w, "No active page", http.StatusNotFound)
			return
		}

		// Get cached readability output
		var readabilityJSON string
		err = db.QueryRow("SELECT readability_output FROM page_cache WHERE url = ?", activeURL).Scan(&readabilityJSON)
		if err != nil {
			logger.Error("Error getting cached content", "error", err)
			http.Error(w, "Content not found", http.StatusNotFound)
			return
		}

		var readabilityResponse readability.ReadabilityResponseSuccess
		if err := json.Unmarshal([]byte(readabilityJSON), &readabilityResponse); err != nil {
			logger.Error("Error unmarshaling readability output", "error", err)
			http.Error(w, "Invalid content format", http.StatusInternalServerError)
			return
		}

		data := struct {
			Title   string
			Content template.HTML
			Excerpt string
		}{
			Title:   readabilityResponse.Title,
			Content: template.HTML(readabilityResponse.Content),
			Excerpt: readabilityResponse.Excerpt,
		}

		if err := tmpl.Execute(w, data); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

func handleLogout(sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session, err := sessionStore.Get(r, "session-name")
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

// func handleLibraryGet(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
// 	tmpl := template.Must(template.ParseFiles(filepath.Join("web", "library.html")))

// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		session, err := sessionStore.Get(r, "session-name")
// 		if err != nil {
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 			return
// 		}

// 		username, ok := session.Values["username"].(string)
// 		if !ok {
// 			http.Error(w, "User not found in session", http.StatusInternalServerError)
// 			return
// 		}

// 		// Get user ID
// 		var userId int
// 		err = db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&userId)
// 		if err != nil {
// 			logger.Error("Error getting user ID", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}

// 		// Get active URL if exists
// 		var activeURL string
// 		err = db.QueryRow("SELECT url FROM user_active_page WHERE user_id = ?", userId).Scan(&activeURL)
// 		if err != nil && err != sql.ErrNoRows {
// 			logger.Error("Error getting active URL", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}

// 		data := struct {
// 			Username  string
// 			ActiveURL string
// 		}{
// 			Username:  username,
// 			ActiveURL: activeURL,
// 		}

// 		if err := tmpl.Execute(w, data); err != nil {
// 			logger.Error("Error executing template", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}
// 	})
// }

// func handleLibraryPost(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore, browser browser.Scraper, readability *readability.Readability) http.Handler {
// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		session, err := sessionStore.Get(r, "session-name")
// 		if err != nil {
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 			return
// 		}

// 		username, ok := session.Values["username"].(string)
// 		if !ok {
// 			http.Error(w, "User not found in session", http.StatusInternalServerError)
// 			return
// 		}

// 		var userId int
// 		err = db.QueryRowContext(r.Context(), "SELECT id FROM users WHERE username = ?", username).Scan(&userId)
// 		if err != nil {
// 			logger.Error("Error getting user ID", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}

// 		url := r.FormValue("url")
// 		if url == "" {
// 			http.Error(w, "URL is required", http.StatusBadRequest)
// 			return
// 		}

// 		tx, err := db.BeginTx(r.Context(), nil)
//         if err != nil {
//             logger.Error("Error starting transaction", "error", err)
//             http.Error(w, "Internal server error", http.StatusInternalServerError)
//             return
//         }
//         defer tx.Rollback()

//         // Insert or get existing page
//         var pageId int
//         err = tx.QueryRowContext(r.Context(), `
//             INSERT INTO pages (user_id, url)
//             VALUES (?, ?)
//             ON CONFLICT (user_id, url)
//             DO UPDATE SET accessed_at = CURRENT_TIMESTAMP
//             RETURNING id`, userId, url).Scan(&pageId)
//         if err != nil {
//             logger.Error("Error inserting page", "error", err)
//             http.Error(w, "Internal server error", http.StatusInternalServerError)
//             return
//         }

//         // Update active page
//         _, err = tx.ExecContext(r.Context(), `
//             INSERT INTO user_active_page (user_id, page_id)
//             VALUES (?, ?)
//             ON CONFLICT(user_id) DO UPDATE SET page_id = excluded.page_id`,
//             userId, pageId)
//         if err != nil {
//             logger.Error("Error updating active page", "error", err)
//             http.Error(w, "Internal server error", http.StatusInternalServerError)
//             return
//         }

//         if err = tx.Commit(); err != nil {
//             logger.Error("Error committing transaction", "error", err)
//             http.Error(w, "Internal server error", http.StatusInternalServerError)
//             return
//         }

//         // Process URL in background
//         go func() {
//             ctx := context.Background()
//             if err := ProcessURL(ctx, url, browser, readability, db, logger); err != nil {
//                 logger.Error("failed to process URL", "error", err, "url", url)
//             }
//         }()

//         http.Redirect(w, r, "/library", http.StatusSeeOther)
// 	})
// }

func addRoutes(mux *http.ServeMux, logger *slog.Logger, readability *readability.ReadabilityClient, db *sql.DB, httpClient *http.Client, sessionStore *sessions.CookieStore) {
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "login.html"))
	})
	mux.Handle("POST /login", handleLoginPost(logger, db, sessionStore))

	mux.HandleFunc("GET /signup", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join("web", "signup.html"))
	})
	mux.Handle("POST /signup", handleSignupPost(logger, db))
	mux.Handle("/logout", handleLogout(sessionStore))

	authMiddleware := newAuthMiddleware(logger, db, sessionStore)

	mux.Handle("GET /read", authMiddleware(handleRead(logger, db, sessionStore)))

	// mux.Handle("GET /library", authMiddleware(handleLibraryGet(logger, db, sessionStore)))
	// mux.Handle("POST /library", authMiddleware(handleLibraryPost(logger, db, sessionStore, browser, readability)))

	mux.Handle("GET /library/pages", authMiddleware(handleLibraryPagesGet(logger, db, sessionStore)))
	mux.Handle("DELETE /library/{id}", authMiddleware(handleLibraryItemDelete(logger, db, sessionStore)))
	mux.Handle("PATCH /library/{id}", authMiddleware(handleLibraryItemPatch(logger, db, sessionStore)))
	mux.Handle("GET /library", authMiddleware(handleLibraryGet(logger, db, sessionStore)))
	mux.Handle("POST /library", authMiddleware(handleLibraryPost(logger, db, sessionStore, httpClient, readability)))

	corsMiddleware := newExtensionCORSMiddleware(logger)
	mux.Handle("GET /ext/check-auth", corsMiddleware(handleExtensionCheckAuth(logger, sessionStore)))
	mux.Handle("POST /ext/article", corsMiddleware(handleExtensionPostContent(logger, db, sessionStore)))

	// Default 404 handler
	mux.Handle("/", authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/library", http.StatusSeeOther)
	})))
}

func handleCheckAuth(logger *slog.Logger, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("auth middleware invoked")

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

		// Log the username
		// username, ok := session.Values["username"].(string)
		// if !ok {
		// 	logger.Error("Username not found in session")
		// 	w.WriteHeader(http.StatusUnauthorized)
		// 	return
		// }

		// logger.Info("User authenticated", "username", username)

		w.WriteHeader(http.StatusOK)
	})
}

// newAuthMiddleware creates the auth checking middleware
func newAuthMiddleware(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) func(h http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, err := sessionStore.Get(r, "read-elsewhere")
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if auth, ok := session.Values["authenticated"].(bool); !ok || !auth {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

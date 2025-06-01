package server

import (
	"context"
	"database/sql"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/egemengol/ereader/internal/readability"
	"github.com/gorilla/sessions"
)

type Page struct {
	ID       int
	URL      string
	Title    string
	IsActive bool
}

func getUserId(r *http.Request, db *sql.DB, sessionStore *sessions.CookieStore) (int, string, error) {
	session, err := sessionStore.Get(r, "read-elsewhere")
	if err != nil {
		return 0, "", err
	}

	username, ok := session.Values["username"].(string)
	if !ok {
		return 0, "", fmt.Errorf("user not found in session")
	}

	var userId int
	err = db.QueryRow("SELECT id FROM users WHERE username = ?", username).Scan(&userId)
	if err != nil {
		return 0, "", err
	}

	return userId, username, nil
}

func getPages(db *sql.DB, userId int) ([]Page, error) {
	rows, err := db.Query(`
        SELECT p.id, p.url,
               COALESCE(json_extract(pc.readability_output, '$.title'), p.url) as title,
               CASE WHEN uap.page_id IS NOT NULL THEN 1 ELSE 0 END as is_active
        FROM pages p
        LEFT JOIN user_active_page uap ON p.id = uap.page_id AND uap.user_id = ?
        LEFT JOIN page_cache pc ON p.url = pc.url
        WHERE p.user_id = ?
        ORDER BY p.accessed_at DESC
    `, userId, userId)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pages []Page
	for rows.Next() {
		var page Page
		if err := rows.Scan(&page.ID, &page.URL, &page.Title, &page.IsActive); err != nil {
			return nil, err
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// GET /library
func handleLibraryGet(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	tmpl := template.Must(template.ParseFiles(
		filepath.Join("web", "library.html"),
		filepath.Join("web", "library-item.html"),
	))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		userId, username, err := getUserId(r, db, sessionStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		pages, err := getPages(db, userId)
		if err != nil {
			logger.Error("Error getting pages", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		data := struct {
			Username string
			Pages    []Page
		}{
			Username: username,
			Pages:    pages,
		}

		if err := tmpl.ExecuteTemplate(w, "library", data); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

func handleLibraryPagesGet(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	tmpl := template.Must(template.ParseFiles(filepath.Join("web", "library-item.html")))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userId, _, err := getUserId(r, db, sessionStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		pages, err := getPages(db, userId)
		if err != nil {
			logger.Error("Error getting pages", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		for _, page := range pages {
			if err := tmpl.ExecuteTemplate(w, "library-item", page); err != nil {
				logger.Error("Error executing template", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
		}
	})
}

// POST /library - Add new page
func handleLibraryPost(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore, httpClient *http.Client, readability *readability.ReadabilityClient) http.Handler {
	tmpl := template.Must(template.ParseFiles(filepath.Join("web", "library-item.html")))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userId, _, err := getUserId(r, db, sessionStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		url := r.FormValue("url")
		if url == "" {
			http.Error(w, "URL is required", http.StatusBadRequest)
			return
		}

		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			logger.Error("Error starting transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// Insert new page and make it active
		var pageId int
		err = tx.QueryRowContext(r.Context(), `
            INSERT INTO pages (user_id, url)
            VALUES (?, ?)
            RETURNING id`, userId, url).Scan(&pageId)
		if err != nil {
			logger.Error("Error inserting page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

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

		if err = tx.Commit(); err != nil {
			logger.Error("Error committing transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Get the title from page_cache if available
		var title string
		err = db.QueryRow(`
            SELECT COALESCE(json_extract(pc.readability_output, '$.title'), ?) as title
            FROM pages p
            LEFT JOIN page_cache pc ON p.url = pc.url
            WHERE p.id = ?`,
			url, pageId).Scan(&title)
		if err != nil {
			logger.Error("Error getting title", "error", err)
			title = url // fallback to URL if error
		}

		// Return just the new page item HTML
		page := Page{ID: pageId, URL: url, IsActive: true, Title: title}
		if err := tmpl.ExecuteTemplate(w, "library-item", page); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Process URL in background
		go func() {
			ctx := context.Background()
			if err := ProcessURL(ctx, url, httpClient, readability, db, logger); err != nil {
				logger.Error("failed to process URL", "error", err, "url", url)
			}
		}()
	})
}

// PATCH /library - Set active page
func handleLibraryItemPatch(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userId, _, err := getUserId(r, db, sessionStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		pageId := r.PathValue("id")
		if pageId == "" {
			http.Error(w, "Page ID is required", http.StatusBadRequest)
			return
		}

		_, err = db.Exec(`
            INSERT INTO user_active_page (user_id, page_id)
            VALUES (?, ?)
            ON CONFLICT(user_id) DO UPDATE SET page_id = excluded.page_id`,
			userId, pageId)
		if err != nil {
			logger.Error("Error activating page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
	})
}

// DELETE /library/{id} - Delete a page
// func handleLibraryItemDelete(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
// 	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 		userId, _, err := getUserId(r, db, sessionStore)
// 		if err != nil {
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 			return
// 		}

// 		// Get page ID using the new PathValue method
// 		pageId := r.PathValue("id")
// 		if pageId == "" {
// 			http.Error(w, "Page ID is required", http.StatusBadRequest)
// 			return
// 		}

// 		// Delete the page, ensuring it belongs to the current user
// 		result, err := db.Exec(`
//             DELETE FROM pages
//             WHERE id = ? AND user_id = ?`,
// 			pageId, userId)
// 		if err != nil {
// 			logger.Error("Error deleting page", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}

// 		// Check if any row was actually deleted
// 		rowsAffected, err := result.RowsAffected()
// 		if err != nil {
// 			logger.Error("Error checking rows affected", "error", err)
// 			http.Error(w, "Internal server error", http.StatusInternalServerError)
// 			return
// 		}
// 		if rowsAffected == 0 {
// 			http.Error(w, "Page not found", http.StatusNotFound)
// 			return
// 		}

// 		// HTMX will handle removing the element from the DOM
// 		w.WriteHeader(http.StatusOK)
// 	})
// }

func handleLibraryItemDelete(logger *slog.Logger, db *sql.DB, sessionStore *sessions.CookieStore) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userId, _, err := getUserId(r, db, sessionStore)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		pageId := r.PathValue("id")
		if pageId == "" {
			http.Error(w, "Page ID is required", http.StatusBadRequest)
			return
		}

		tx, err := db.BeginTx(r.Context(), nil)
		if err != nil {
			logger.Error("Error starting transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// Check if this is the active page
		var isActive bool
		err = tx.QueryRow(`
            SELECT EXISTS (
                SELECT 1 FROM user_active_page
                WHERE user_id = ? AND page_id = ?
            )`, userId, pageId).Scan(&isActive)
		if err != nil {
			logger.Error("Error checking active page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Delete the page
		result, err := tx.Exec(`
            DELETE FROM pages
            WHERE id = ? AND user_id = ?`,
			pageId, userId)
		if err != nil {
			logger.Error("Error deleting page", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		rowsAffected, err := result.RowsAffected()
		if err != nil || rowsAffected == 0 {
			logger.Error("Error checking rows affected", "error", err)
			http.Error(w, "Page not found", http.StatusNotFound)
			return
		}

		if isActive {
			// Set the most recent page as active
			_, err = tx.Exec(`
                UPDATE user_active_page
                SET page_id = (
                    SELECT id
                    FROM pages
                    WHERE user_id = ?
                    ORDER BY accessed_at DESC
                    LIMIT 1
                )
                WHERE user_id = ?`,
				userId, userId)
			if err != nil && err != sql.ErrNoRows {
				logger.Error("Error updating active page", "error", err)
				http.Error(w, "Internal server error", http.StatusInternalServerError)
				return
			}
		}

		if err = tx.Commit(); err != nil {
			logger.Error("Error committing transaction", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		if isActive {
			w.Header().Set("HX-Trigger", "activePageDeleted")
		}
		w.WriteHeader(http.StatusOK)
	})
}

package server

import (
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/egemengol/kindlepathy/internal/core"
	db "github.com/egemengol/kindlepathy/internal/db/generated"
)

//go:embed library.html
var TEMPLATE_LIBRARY string

// GET /library
func handleLibraryGet(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	tmpl := template.Must(template.New("library").Parse(TEMPLATE_LIBRARY))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		items, err := c.ListItems(r.Context(), authedUser.ID)
		if err != nil {
			logger.Error("Error listing items", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		data := struct {
			Items []core.Item
		}{
			Items: items,
		}

		if err := tmpl.ExecuteTemplate(w, "library", data); err != nil {
			logger.Error("Error executing template", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	})
}

// POST /library - Add new item
func handleLibraryPost(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "Failed to parse form", http.StatusBadRequest)
			return
		}

		url := r.Form.Get("url")
		if url == "" {
			http.Error(w, "URL is required", http.StatusBadRequest)
			return
		}

		_, err = c.AddItemWithTitleSetActive(r.Context(), authedUser.ID, url, time.Now())
		if err != nil {
			logger.Error("Error adding item", "error", err, "url", url)
			http.Error(w, "Failed to add item", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, "/library", http.StatusSeeOther)
	})
}

// PATCH /library - Set active item
func handleLibraryItemPatch(auth *AuthService, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		itemId := r.PathValue("id")
		if itemId == "" {
			http.Error(w, "Item ID is required", http.StatusBadRequest)
			return
		}

		itemIdInt64, err := strconv.ParseInt(itemId, 10, 64)
		if err != nil {
			http.Error(w, "Invalid item ID", http.StatusBadRequest)
			return
		}

		err = auth.queries.UsersSetActiveItem(r.Context(), db.UsersSetActiveItemParams{
			ActiveItemID: itemIdInt64,
			ID:           authedUser.ID,
		})
		if err != nil {
			logger.Error("Error activating item", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Check if request is from HTMX
		if r.Header.Get("HX-Request") != "" {
			w.WriteHeader(http.StatusOK)
		} else {
			// Redirect to the current URL for non-HTMX requests
			http.Redirect(w, r, r.RequestURI, http.StatusSeeOther)
		}
	})
}

// DELETE /library/{id} - Delete item
func handleLibraryItemDelete(c *core.Core, auth *AuthService, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authedUser, err := auth.GetAuthenticatedUser(r)
		if err != nil {
			auth.HandleAuthError(w, r, err)
			return
		}

		itemId := r.PathValue("id")
		if itemId == "" {
			http.Error(w, "Item ID is required", http.StatusBadRequest)
			return
		}

		itemIdInt64, err := strconv.ParseInt(itemId, 10, 64)
		if err != nil {
			http.Error(w, "Invalid item ID", http.StatusBadRequest)
			return
		}

		// Check if item belongs to user first
		item, err := auth.queries.ItemsGet(r.Context(), itemIdInt64)
		if err != nil {
			logger.Error("Error getting item", "error", err)
			http.Error(w, "Item not found", http.StatusNotFound)
			return
		}

		if item.UserID != authedUser.ID {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		err = c.DeleteItem(r.Context(), itemIdInt64)
		if err != nil {
			logger.Error("Error deleting item", "error", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Check if request is from HTMX
		if r.Header.Get("HX-Request") != "" {
			w.WriteHeader(http.StatusOK)
		} else {
			// Redirect to library for non-HTMX requests
			http.Redirect(w, r, "/library", http.StatusSeeOther)
		}
	})
}

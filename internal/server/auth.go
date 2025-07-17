package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/egemengol/kindlepathy/internal/db/generated"
	"github.com/gorilla/sessions"
)

type contextKey string

const userContextKey = contextKey("user")

type AuthenticatedUser struct {
	ID           int64
	Username     string
	ActiveItemID *int64
}

type AuthService struct {
	queries      *db.Queries
	sessionStore *sessions.CookieStore
}

func NewAuthService(queries *db.Queries, sessionStore *sessions.CookieStore) *AuthService {
	return &AuthService{
		queries:      queries,
		sessionStore: sessionStore,
	}
}

// GetAuthenticatedUser extracts user information from the request context.
func (a *AuthService) GetAuthenticatedUser(r *http.Request) (AuthenticatedUser, error) {
	user, ok := r.Context().Value(userContextKey).(AuthenticatedUser)
	if !ok {
		return AuthenticatedUser{}, fmt.Errorf("user not found in context")
	}
	return user, nil
}

// RequireOwnership checks if the user owns the specified item
func (a *AuthService) RequireOwnership(ctx context.Context, username string, itemID int64) error {
	doesOwn, err := a.queries.UsersOwnsItem(ctx, db.UsersOwnsItemParams{
		Username: username,
		ID:       itemID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("item not found")
		}
		return fmt.Errorf("failed to check ownership: %w", err)
	}
	if doesOwn == 0 {
		return fmt.Errorf("you do not own this item")
	}
	return nil
}

// HandleAuthError provides standardized auth error responses
func (a *AuthService) HandleAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if err.Error() == "user not found in context" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err.Error() == "session user not found in database" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err.Error() == "user not found in session" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err.Error() == "you do not own this item" {
		http.Error(w, "You do not own this item", http.StatusForbidden)
		return
	}
	if err.Error() == "item not found" {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}

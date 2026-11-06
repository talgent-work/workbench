package authn

import (
	"context"
	"strings"
)

type authenticatedUserContextKey struct{}

// AuthenticatedUser contains trusted gateway-supplied user identity claims.
type AuthenticatedUser struct {
	ID           string
	Username     string
	DisplayName  string
	PrimaryEmail string
	AvatarURL    string
	Roles        []string
}

// WithAuthenticatedUser returns a context carrying an authenticated user.
func WithAuthenticatedUser(ctx context.Context, user AuthenticatedUser) context.Context {
	id, ok := normalizeUserID(user.ID)
	if !ok {
		return ctx
	}
	user.ID = id
	user.Username = strings.TrimSpace(user.Username)
	user.DisplayName = strings.TrimSpace(user.DisplayName)
	user.PrimaryEmail = strings.TrimSpace(user.PrimaryEmail)
	user.AvatarURL = strings.TrimSpace(user.AvatarURL)
	return context.WithValue(ctx, authenticatedUserContextKey{}, user)
}

// CurrentUser returns the authenticated user from ctx.
func CurrentUser(ctx context.Context) (AuthenticatedUser, bool) {
	user, ok := ctx.Value(authenticatedUserContextKey{}).(AuthenticatedUser)
	id, valid := normalizeUserID(user.ID)
	user.ID = id
	return user, ok && valid
}

// CurrentUserID returns the authenticated product user id from ctx.
func CurrentUserID(ctx context.Context) (string, bool) {
	user, ok := CurrentUser(ctx)
	return user.ID, ok
}

func normalizeUserID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	if id == "" || strings.HasPrefix(id, "oidc:") || strings.Contains(id, "#") {
		return "", false
	}
	return id, true
}

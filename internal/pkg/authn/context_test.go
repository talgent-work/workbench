package authn

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthenticatedUserRoundTrip(t *testing.T) {
	// Authorization code must see the same user that authentication placed into
	// context, otherwise protected application methods run without an actor.
	ctx := WithAuthenticatedUser(context.Background(), AuthenticatedUser{
		ID:           " sub-1 ",
		Username:     " octocat ",
		DisplayName:  " Octo Cat ",
		PrimaryEmail: " octo@example.test ",
		AvatarURL:    " https://example.test/avatar.png ",
		Roles:        []string{"admin"},
	})

	got, ok := CurrentUser(ctx)
	require.True(t, ok)
	require.Equal(t, "sub-1", got.ID)
	require.Equal(t, "octocat", got.Username)
	require.Equal(t, "Octo Cat", got.DisplayName)
	require.Equal(t, "octo@example.test", got.PrimaryEmail)
	require.Equal(t, "https://example.test/avatar.png", got.AvatarURL)

	id, ok := CurrentUserID(ctx)
	require.True(t, ok)
	require.Equal(t, "sub-1", id)
}

func TestAuthenticatedUserRejectsInvalidIDs(t *testing.T) {
	// Product identity must be the bare OIDC subject. Composite or empty
	// IDs must not mark a request as authenticated.
	for name, userID := range map[string]string{
		"empty":           "",
		"prefixed user":   "oidc:https://auth.example#sub-1",
		"contains marker": "issuer#sub-1",
	} {
		t.Run(name, func(t *testing.T) {
			ctx := WithAuthenticatedUser(context.Background(), AuthenticatedUser{ID: userID})

			got, ok := CurrentUserID(ctx)
			require.False(t, ok)
			require.Empty(t, got)
		})
	}
}

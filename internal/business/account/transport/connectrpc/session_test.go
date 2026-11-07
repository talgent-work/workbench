package connectrpc

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"

	accountpublicv1 "github.com/talgent-work/contracts/gen/account/public/v1"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// The session is the gateway-verified identity, and nothing without one.
func TestTheSessionIsTheVerifiedIdentity(t *testing.T) {
	_, err := Session{}.GetCurrentSession(context.Background(), &accountpublicv1.GetCurrentSessionRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	id := "0b5f5b7e-4c1a-4f6e-9d55-6f2b0a8a9c11"
	ctx := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: id, Username: "ada", DisplayName: "Ada", PrimaryEmail: "ada@example.com", AvatarURL: "https://example.com/a.png", Roles: []string{"member"}})
	response, err := Session{}.GetCurrentSession(ctx, &accountpublicv1.GetCurrentSessionRequest{})
	require.NoError(t, err)
	require.Equal(t, &accountpublicv1.CurrentSession{UserId: id, Username: "ada", DisplayName: "Ada", PrimaryEmail: "ada@example.com", AvatarUrl: "https://example.com/a.png", Roles: []string{"member"}}, response.GetSession())
}

package connectrpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// A stream is read for the person or the Application the request speaks for,
// which Agent then admits to its own Works.
func TestStreamReaderIsThePersonOrTheApplication(t *testing.T) {
	const person, application = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c51", "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
	ctx := authn.WithPrincipal(context.Background(), authn.ApplicationPrincipal(authn.Application{ID: application}))
	require.Equal(t, application, streamReader(ctx))
	ctx = authn.WithPrincipal(context.Background(), authn.UserPrincipal(authn.AuthenticatedUser{ID: person}))
	require.Equal(t, person, streamReader(ctx))
	require.Empty(t, streamReader(context.Background()), "no one is read for when the request speaks for no one")
}

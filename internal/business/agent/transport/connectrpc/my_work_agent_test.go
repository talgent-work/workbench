package connectrpc

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// A new Work Agent is named after the person's display name, else their
// username.
func TestAWorkAgentIsNamedAfterItsPerson(t *testing.T) {
	require.Equal(t, "Ada", nameOf(authn.AuthenticatedUser{DisplayName: " Ada ", Username: "ada"}))
	require.Equal(t, "ada", nameOf(authn.AuthenticatedUser{Username: "ada"}))
}

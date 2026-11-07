package storage

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

// The core starts with Agent as its only caller; callers beyond it, such as
// an extension's, are configured only when deployed.
func TestConfigNeedsOnlyAgentAmongItsCallers(t *testing.T) {
	config := Config{PersistentRoot: "/p", WorkingRoot: "/w", LinkSecret: "s", AgentToken: "a",
		Mounts: MountConfig{WorkingClaim: "nas", InputsClaim: "inputs"}, Preview: PreviewConfig{GotenbergAddress: "http://gotenberg"}}
	require.NoError(t, config.Validate())
	config.AgentToken = " "
	require.Error(t, config.Validate())
}

// A Work without a Session Workspace sends up to 64 MiB to publish an
// Artifact unless a test lowers the bound; cases exercise the bound lowered.
func TestTheSentContentBoundIs64MiB(t *testing.T) {
	require.Equal(t, 64<<20, domain.MaxSentContentBytes)
	config := Config{PersistentRoot: t.TempDir(), WorkingRoot: t.TempDir(), LinkSecret: "s", AgentToken: "a",
		Mounts: MountConfig{WorkingClaim: "nas", InputsClaim: "inputs"}, Preview: PreviewConfig{GotenbergAddress: "http://gotenberg"}}
	app, err := assemble(config, nil, Rendering{}, nil, slog.Default())
	require.NoError(t, err)
	require.Equal(t, domain.MaxSentContentBytes, app.SentContentLimit())
}

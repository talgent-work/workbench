package storage

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/storage/domain"
)

func validConfig(t *testing.T) Config {
	return Config{PersistentRoot: t.TempDir(), WorkingRoot: t.TempDir(), LinkSecret: "s", AgentToken: "a",
		Mounts: MountConfig{NASClaim: "nas", JuiceFSClaim: "juicefs"}, Preview: PreviewConfig{GotenbergAddress: "http://gotenberg"}}
}

// The core starts with Agent as its only caller; callers beyond it, such as
// an extension's, are configured only when deployed.
func TestConfigNeedsOnlyAgentAmongItsCallers(t *testing.T) {
	config := validConfig(t)
	require.NoError(t, config.Validate())
	config.AgentToken = " "
	require.Error(t, config.Validate())
	config = validConfig(t)
	config.Mounts.JuiceFSClaim = ""
	require.Error(t, config.Validate(), "every backend's claim is named")
}

// A Work without a Session Workspace sends up to 64 MiB to publish an
// Artifact unless a test lowers the bound; cases exercise the bound lowered.
func TestTheSentContentBoundIs64MiB(t *testing.T) {
	require.Equal(t, 64<<20, domain.MaxSentContentBytes)
	app, err := assemble(validConfig(t), nil, nil, nil, nil, slog.Default())
	require.NoError(t, err)
	require.Equal(t, domain.MaxSentContentBytes, app.SentContentLimit())
}

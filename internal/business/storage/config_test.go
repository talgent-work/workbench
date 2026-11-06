package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
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

package agent_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/sealing/sealingtest"
)

// personalVariableConfig locates a test-only Agent key for a composed
// module.
func personalVariableConfig(t testing.TB) agent.PersonalVariableConfig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "personal-variable-key")
	require.NoError(t, os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(sealingtest.Key)), 0o600))
	return agent.PersonalVariableConfig{KeyPath: path}
}

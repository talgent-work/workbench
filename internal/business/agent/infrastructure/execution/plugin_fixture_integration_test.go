//go:build integration

package execution

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// These runtime tests control package bytes locally. Remote immutable source
// retrieval is covered by the Marketplace native synchronization integration.
func installFixturePlugin(t *testing.T, ctx context.Context, launch nativeLaunch, request domain.StartExecutionRequest, workspace, plugin string, environment []string) []pluginInstallationResult {
	t.Helper()
	root := filepath.Join(workspace, ".claude", "talgent", "talgent-work-"+storageKey(request.WorkID))
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.CopyFS(filepath.Join(root, "fixture"), os.DirFS(plugin)))
	process := exec.CommandContext(ctx, "sh", "-c", launch.PreparationCommand)
	process.Dir, process.Env = workspace, environment
	output, err := process.Output()
	require.NoError(t, err)
	return installationResults(output, 0, request.Capabilities)
}

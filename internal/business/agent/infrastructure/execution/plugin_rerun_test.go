package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// execd starts a finished session again when a holder attaches to it, so the
// preparation of one allocation must not install twice.
func TestPreparationRerunOfTheSameAllocationDoesNotInstallAgain(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to run the native preparation adapter")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	claude := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(claude, []byte("#!/bin/sh\necho \"$*\" >> "+calls+"\n"), 0o755))
	workspace := filepath.Join(dir, "workspace")
	require.NoError(t, os.MkdirAll(workspace, 0o700))
	input := request()
	input.Capabilities.Plugins = []domain.SelectedPlugin{{Release: domain.PluginRelease{Slug: "fixed", Source: "fixture", Version: "1", RetrievalRef: "archive:fixed", NativeEntry: []byte(`{"name":"fixed","version":"1"}`)}}}
	config := NativeLaunchConfig{Binary: claude}
	run := func(allocation string) (string, int) {
		launch, err := config.prepare(sandboxLayout, input, SandboxOwner{WorkID: input.WorkID, ID: allocation}, "")
		require.NoError(t, err)
		process := exec.Command("sh", "-c", launch.PreparationCommand)
		process.Dir, process.Env = workspace, append(os.Environ(), "HOME="+filepath.Join(dir, "home"), "CLAUDE_CONFIG_DIR="+filepath.Join(dir, "home", ".claude"))
		output, err := process.Output()
		require.NoError(t, err)
		recorded, _ := os.ReadFile(calls)
		return string(output), strings.Count(string(recorded), "\n")
	}
	first, installs := run(fixtureAllocationID)
	require.Equal(t, []domain.PluginInstallation{{Slug: "fixed", Installed: true}}, installationResults([]byte(first), 0, input.Capabilities))
	require.Equal(t, 3, installs, "marketplace add, one install and its update")

	again, reruns := run(fixtureAllocationID)
	require.Equal(t, first, again, "a re-run reports the finished preparation")
	require.Equal(t, installs, reruns, "a re-run of the same allocation installs nothing")

	_, restarted := run("01900000-0000-7000-8000-000000000002")
	require.Greater(t, restarted, reruns, "a new allocation (Restart) prepares again")
}

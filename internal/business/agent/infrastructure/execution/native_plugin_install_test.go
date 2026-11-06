package execution

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A restored sandbox keeps its HOME and workspace, so a Plugin installed by an
// earlier preparation is already recorded at its old version. Native install
// treats it as installed and keeps that version; the preparation also updates
// it, so the session loads the release the Work's manifest pins.
func TestPreparationUpdatesAnInstalledPluginToThePinnedRelease(t *testing.T) {
	requireNodeAndFlock(t)
	// The adapter names the marketplace by its working directory's real path.
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	calls := filepath.Join(t.TempDir(), "calls")
	results := runPluginPreparation(t, workspace, fakeClaude(t, calls, ""), fixtureAllocationID)
	require.Equal(t, []pluginInstallationResult{{Slug: "talgent-runtime", Installed: true}}, results)
	require.Equal(t, []string{
		"plugin marketplace add --scope project " + filepath.Join(workspace, ".claude", "talgent", "talgent-work-fixture"),
		"plugin install --scope project talgent-runtime@talgent-work-fixture",
		"plugin update --scope project talgent-runtime@talgent-work-fixture",
	}, strings.Split(strings.TrimSpace(readFile(t, calls)), "\n"))
	require.Equal(t, map[string]bool{"talgent-runtime@talgent-work-fixture": true}, enabledPlugins(t, workspace))
}

// A Plugin that cannot reach its pinned release is not reported installed:
// the version it would load is not the Work's.
func TestPreparationReportsAPluginThatCannotUpdateAsNotInstalled(t *testing.T) {
	requireNodeAndFlock(t)
	workspace := t.TempDir()
	results := runPluginPreparation(t, workspace, fakeClaude(t, filepath.Join(t.TempDir(), "calls"), "update"), fixtureAllocationID)
	require.Equal(t, []pluginInstallationResult{{Slug: "talgent-runtime", Reason: "installation_failed"}}, results)
	require.Equal(t, map[string]bool{"talgent-runtime@talgent-work-fixture": false}, enabledPlugins(t, workspace))
}

func runPluginPreparation(t *testing.T, workspace, binary, allocation string) []pluginInstallationResult {
	t.Helper()
	catalog := `{"name":"talgent-work-fixture","owner":{"name":"Talgent"},"plugins":[{"name":"talgent-runtime","version":"0.1.1","source":{"source":"url","url":"https://example.com/marketplace.git","sha":"0123456789abcdef0123456789abcdef01234567"}}]}`
	receipt := storageKey(request().WorkID + ":" + allocation)[:32]
	process := exec.Command("node", "-e", nativePreparation, binary, catalog, receipt, "", "600")
	process.Dir, process.Env = workspace, append(os.Environ(), "HOME="+t.TempDir())
	output, err := process.Output()
	require.NoError(t, err)
	var report preparationReport
	require.NoError(t, json.Unmarshal(output, &report), string(output))
	return report.Results
}

// fakeClaude records each invocation's arguments and fails the plugin
// subcommand named by failing.
func fakeClaude(t *testing.T, calls, failing string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\necho \"$*\" >> '" + calls + "'\n"
	if failing != "" {
		script += "test \"$2\" = '" + failing + "' && exit 1\n"
	}
	require.NoError(t, os.WriteFile(binary, []byte(script+"exit 0\n"), 0o700))
	return binary
}

func enabledPlugins(t *testing.T, workspace string) map[string]bool {
	t.Helper()
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	require.NoError(t, json.Unmarshal([]byte(readFile(t, filepath.Join(workspace, ".claude", "settings.local.json"))), &settings))
	return settings.EnabledPlugins
}

// The install lock uses util-linux flock, as the sandbox image provides.
func requireNodeAndFlock(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is required to run the native preparation adapter")
	}
	if exec.Command("flock", "--no-fork", "-w", "1", filepath.Join(t.TempDir(), "lock"), "true").Run() != nil {
		t.Skip("util-linux flock is required to run the native Plugin installation")
	}
}

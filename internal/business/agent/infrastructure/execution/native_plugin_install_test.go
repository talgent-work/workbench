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

// The fixture Work selects the Plugin talgent at release 0.1.1 from the
// catalog talgent.
const (
	fixturePlugin  = "talgent@talgent"
	fixtureRelease = "0.1.1"
)

// stateFixture is a temporary Harness state whose plugin state the
// preparation reads, and the workspace a Work runs in.
type stateFixture struct {
	workspace, home string
}

func newStateFixture(t *testing.T) stateFixture {
	t.Helper()
	requireNodeAndFlock(t)
	// The adapter names the marketplace by its working directory's real path.
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	return stateFixture{workspace: workspace, home: t.TempDir()}
}

func (s stateFixture) plugins() string { return filepath.Join(s.home, ".claude", "plugins") }

// release is the package directory of a release in the Harness state's cache.
func (s stateFixture) release(marketplace, plugin, version string) string {
	return filepath.Join(s.plugins(), "cache", marketplace, plugin, version)
}

// cache places a release's package in the Harness state's cache.
func (s stateFixture) cache(t *testing.T, marketplace, plugin, version string) string {
	t.Helper()
	directory := s.release(marketplace, plugin, version)
	require.NoError(t, os.MkdirAll(directory, 0o700))
	return directory
}

// record writes Claude Code's install records: each id recorded for a
// project path at a version and package directory.
func (s stateFixture) record(t *testing.T, records map[string][]installRecord) {
	t.Helper()
	require.NoError(t, os.MkdirAll(s.plugins(), 0o700))
	body, err := json.Marshal(map[string]any{"version": 2, "plugins": records})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(s.plugins(), "installed_plugins.json"), body, 0o600))
}

// register writes Claude Code's marketplace registrations.
func (s stateFixture) register(t *testing.T, names ...string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(s.plugins(), 0o700))
	registrations := map[string]any{}
	for _, name := range names {
		registrations[name] = map[string]any{"source": map[string]string{"source": "directory", "path": "/workspace/.claude/talgent/" + name}}
	}
	body, err := json.Marshal(registrations)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(s.plugins(), "known_marketplaces.json"), body, 0o600))
}

type installRecord struct {
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
	InstallPath string `json:"installPath"`
	Version     string `json:"version"`
}

// recordedHere is this workspace's install record of the fixture Plugin at
// version, its package in the cache.
func (s stateFixture) recordedHere(t *testing.T, version string) installRecord {
	t.Helper()
	return installRecord{Scope: "project", ProjectPath: s.workspace, InstallPath: s.cache(t, "talgent", "talgent", version), Version: version}
}

// prepare runs the Work's preparation with a fake claude that records each
// invocation and fails the plugin subcommand named by failing.
func (s stateFixture) prepare(t *testing.T, failing string) ([]pluginInstallationResult, []string) {
	t.Helper()
	calls := filepath.Join(t.TempDir(), "calls")
	results := runPluginPreparationIn(t, s.workspace, s.home, fakeClaude(t, calls, failing), fixtureAllocationID)
	data, err := os.ReadFile(calls)
	if os.IsNotExist(err) {
		return results, nil
	}
	require.NoError(t, err)
	return results, strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (s stateFixture) marketplaceAdd() string {
	return "plugin marketplace add --scope project " + filepath.Join(s.workspace, ".claude", "talgent", "talgent")
}

var installedFixture = []pluginInstallationResult{{Slug: "talgent", Installed: true}}

// A later Work of the definition finds the release an earlier Work installed
// recorded for its workspace path, with its package present: it loads it
// without a native install, and only enables it.
func TestPreparationLoadsARecordedReleaseWithoutInstalling(t *testing.T) {
	state := newStateFixture(t)
	state.record(t, map[string][]installRecord{fixturePlugin: {state.recordedHere(t, fixtureRelease)}})
	results, calls := state.prepare(t, "")
	require.Equal(t, installedFixture, results)
	require.Equal(t, []string{state.marketplaceAdd()}, calls)
	require.Equal(t, map[string]bool{fixturePlugin: true}, enabledPlugins(t, state.workspace))
	require.DirExists(t, state.release("talgent", "talgent", fixtureRelease))
}

// A release the Harness state has not installed for this workspace, or whose
// recorded package the cache lost, is installed as before.
func TestPreparationInstallsAnUnrecordedReleaseOrAMissingPackage(t *testing.T) {
	for name, arrange := range map[string]func(stateFixture){
		"unrecorded": func(stateFixture) {},
		"recorded for another workspace": func(state stateFixture) {
			other := state.recordedHere(t, fixtureRelease)
			other.ProjectPath = "/elsewhere"
			state.record(t, map[string][]installRecord{fixturePlugin: {other}})
		},
		"package missing": func(state stateFixture) {
			record := state.recordedHere(t, fixtureRelease)
			require.NoError(t, os.RemoveAll(record.InstallPath))
			state.record(t, map[string][]installRecord{fixturePlugin: {record}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := newStateFixture(t)
			arrange(state)
			results, calls := state.prepare(t, "")
			require.Equal(t, installedFixture, results)
			require.Equal(t, []string{state.marketplaceAdd(), "plugin install --scope project " + fixturePlugin}, calls)
			require.Equal(t, map[string]bool{fixturePlugin: true}, enabledPlugins(t, state.workspace))
		})
	}
}

// A Plugin recorded at another release is installed, which keeps the
// recorded version, then updated to the release the manifest names; records
// that cannot be read may hide another release, so they take both.
func TestPreparationUpdatesAPluginRecordedAtAnotherRelease(t *testing.T) {
	for name, arrange := range map[string]func(*testing.T, stateFixture){
		"older release": func(t *testing.T, state stateFixture) {
			state.record(t, map[string][]installRecord{fixturePlugin: {state.recordedHere(t, "0.1.0")}})
		},
		"unreadable records": func(t *testing.T, state stateFixture) {
			require.NoError(t, os.MkdirAll(state.plugins(), 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(state.plugins(), "installed_plugins.json"), []byte("{not json"), 0o600))
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := newStateFixture(t)
			arrange(t, state)
			results, calls := state.prepare(t, "")
			require.Equal(t, installedFixture, results)
			require.Equal(t, []string{state.marketplaceAdd(), "plugin install --scope project " + fixturePlugin, "plugin update --scope project " + fixturePlugin}, calls)
		})
	}
}

// A Plugin that cannot reach the resolved release is not reported installed,
// and stays disabled.
func TestPreparationReportsAPluginThatCannotUpdateAsNotInstalled(t *testing.T) {
	state := newStateFixture(t)
	state.record(t, map[string][]installRecord{fixturePlugin: {state.recordedHere(t, "0.1.0")}})
	results, _ := state.prepare(t, "update")
	require.Equal(t, []pluginInstallationResult{{Slug: "talgent", Reason: "installation_failed"}}, results)
	require.Equal(t, map[string]bool{fixturePlugin: false}, enabledPlugins(t, state.workspace))
}

// The Harness state keeps only the releases still recorded: a release
// directory of the catalog's marketplace that no install record references,
// for any workspace, is removed, and one that a record references is kept.
func TestPreparationRemovesReleasesNoRecordReferences(t *testing.T) {
	state := newStateFixture(t)
	other := installRecord{Scope: "project", ProjectPath: "/elsewhere", InstallPath: state.cache(t, "talgent", "reviewer", "2.0.0"), Version: "2.0.0"}
	state.record(t, map[string][]installRecord{
		fixturePlugin:            {state.recordedHere(t, fixtureRelease)},
		"reviewer@talgent":       {other},
		"tools@claude-community": {{Scope: "user", InstallPath: state.cache(t, "claude-community", "tools", "1.0.0"), Version: "1.0.0"}},
	})
	state.cache(t, "talgent", "talgent", "0.1.0")
	state.cache(t, "talgent", "reviewer", "1.0.0")
	state.cache(t, "talgent", "retired", "1.0.0")
	state.cache(t, "claude-community", "tools", "0.9.0")

	results, _ := state.prepare(t, "")
	require.Equal(t, installedFixture, results)
	require.DirExists(t, state.release("talgent", "talgent", fixtureRelease))
	require.DirExists(t, state.release("talgent", "reviewer", "2.0.0"), "a release another workspace records stays")
	require.NoDirExists(t, state.release("talgent", "talgent", "0.1.0"))
	require.NoDirExists(t, state.release("talgent", "reviewer", "1.0.0"))
	require.NoDirExists(t, filepath.Join(state.plugins(), "cache", "talgent", "retired"), "a Plugin with no recorded release leaves nothing")
	require.DirExists(t, state.release("claude-community", "tools", "0.9.0"), "another marketplace's packages are not Talgent's to remove")
}

// Marketplaces earlier preparations named per Work are removed: their
// registrations and install records through the native command, and their
// cached packages.
func TestPreparationRemovesPerWorkMarketplaces(t *testing.T) {
	state := newStateFixture(t)
	state.record(t, map[string][]installRecord{fixturePlugin: {state.recordedHere(t, fixtureRelease)}})
	state.register(t, "talgent", "talgent-work-aaa", "talgent-work-bbb", "claude-community")
	state.cache(t, "talgent-work-aaa", "talgent", "0.1.1")
	state.cache(t, "talgent-work-ccc", "talgent", "0.1.0")

	results, calls := state.prepare(t, "")
	require.Equal(t, installedFixture, results)
	require.Equal(t, []string{
		state.marketplaceAdd(),
		"plugin marketplace remove talgent-work-aaa",
		"plugin marketplace remove talgent-work-bbb",
	}, calls)
	require.NoDirExists(t, filepath.Join(state.plugins(), "cache", "talgent-work-aaa"))
	require.NoDirExists(t, filepath.Join(state.plugins(), "cache", "talgent-work-ccc"))
	require.DirExists(t, state.release("talgent", "talgent", fixtureRelease))
}

func runPluginPreparation(t *testing.T, workspace, binary, allocation string) []pluginInstallationResult {
	t.Helper()
	return runPluginPreparationIn(t, workspace, t.TempDir(), binary, allocation)
}

func runPluginPreparationIn(t *testing.T, workspace, home, binary, allocation string) []pluginInstallationResult {
	t.Helper()
	catalog := `{"name":"talgent","owner":{"name":"Talgent"},"plugins":[{"name":"talgent","version":"` + fixtureRelease + `","source":{"source":"url","url":"https://example.com/marketplace.git","sha":"0123456789abcdef0123456789abcdef01234567"}}]}`
	receipt := storageKey(request().WorkID + ":" + allocation)[:32]
	process := exec.Command("node", "-e", nativePreparation, binary, catalog, receipt, "", "600")
	process.Dir, process.Env = workspace, append(os.Environ(), "HOME="+home, "CLAUDE_CONFIG_DIR=", "CLAUDE_CODE_PLUGIN_CACHE_DIR=")
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

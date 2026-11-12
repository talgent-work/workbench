package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// pluginWork is a new Work that selected the Plugins runtime 1.0.0 and tools
// 2.0.0.
func pluginWork(t *testing.T) *domain.AgentWork {
	t.Helper()
	work := newSession(t)
	work.CapabilitySnapshot.Plugins = []domain.SelectedPlugin{
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "runtime", Version: "1.0.0"}},
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "tools", Version: "2.0.0"}},
	}
	return work
}

// boundPluginExecution is the bound execution of pluginWork's start.
func boundPluginExecution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	execution := beginOf(t, pluginWork(t), domain.RealizationCloud)
	require.True(t, commandtest.Submit(execution))
	_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-1"})
	require.NoError(t, err)
	return execution
}

// preparedPluginExecution is a bound execution whose preparation reported
// both Plugins installed and whose Harness process "process-1" was launched.
func preparedPluginExecution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	execution := boundPluginExecution(t)
	changed, err := execution.RecordPreparation(domain.PreparationReport{Installations: []domain.PluginInstallation{
		{Slug: "runtime", Installed: true}, {Slug: "tools", Installed: true},
	}})
	require.NoError(t, err)
	require.True(t, changed)
	launched, err := execution.RecordLaunch("process-1")
	require.NoError(t, err)
	require.True(t, launched)
	return execution
}

// reportLoaded records the inventory process-1 reports.
func reportLoaded(t *testing.T, execution *domain.AgentExecution, loaded ...domain.LoadedPlugin) bool {
	t.Helper()
	changed, err := execution.RecordPreparation(domain.PreparationReport{ProcessID: "process-1", Loaded: loaded})
	require.NoError(t, err)
	return changed
}

func resultOf(t *testing.T, execution *domain.AgentExecution, slug string) domain.PluginResult {
	t.Helper()
	for _, result := range execution.PluginResults {
		if result.Slug == slug {
			return result
		}
	}
	t.Fatalf("no outcome for Plugin %s", slug)
	return domain.PluginResult{}
}

func loadingOf(t *testing.T, execution *domain.AgentExecution, slug string) domain.PluginOutcome {
	t.Helper()
	return resultOf(t, execution, slug).Loading
}

var (
	succeeded = domain.PluginOutcome{Reported: true, Succeeded: true}
	notLoaded = domain.PluginOutcome{Reported: true, Reason: domain.PluginNotLoaded}
)

// The preparation's report records each selected Plugin's installation with
// the end of preparation (Preparation ownership); a Plugin missing from the
// Harness's inventory failed to load, and loading stays apart from
// installation.
func TestPreparationRecordsEachSelectedPluginsInstallationAndLoading(t *testing.T) {
	execution := boundPluginExecution(t)
	changed, err := execution.RecordPreparation(domain.PreparationReport{Installations: []domain.PluginInstallation{
		{Slug: "runtime", Installed: true}, {Slug: "tools", Reason: "installation_failed"},
	}})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, execution.Process.PreparationEnded)
	require.Equal(t, []domain.PluginResult{
		{Slug: "runtime", Installation: succeeded},
		{Slug: "tools", Installation: domain.PluginOutcome{Reported: true, Reason: "installation_failed"}},
	}, execution.PluginResults)

	_, err = execution.RecordLaunch("process-1")
	require.NoError(t, err)
	require.True(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"}))
	require.Equal(t, succeeded, loadingOf(t, execution, "runtime"))
	require.Equal(t, notLoaded, loadingOf(t, execution, "tools"))
	require.Equal(t, succeeded, execution.PluginResults[0].Installation, "loading leaves installation as reported")

	changed, err = execution.RecordPreparation(domain.PreparationReport{Installations: []domain.PluginInstallation{{Slug: "runtime", Reason: "installation_failed"}}})
	require.NoError(t, err)
	require.False(t, changed, "preparation ends once, and its report with it")
	require.Equal(t, succeeded, execution.PluginResults[0].Installation)
}

// The Harness loads the latest release installed in its Harness state, which
// may be newer than the one the run took (Latest loaded release): a Plugin
// the Harness reports loaded counts as loaded at the version it reports,
// none when it reports none (Loading outcome).
func TestAPluginLoadedAtAnotherVersionIsLoadedAtThatVersion(t *testing.T) {
	execution := preparedPluginExecution(t)
	require.True(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "runtime"}, domain.LoadedPlugin{Slug: "tools", Version: "9.9.9"}))
	require.Equal(t, domain.PluginResult{Slug: "runtime", Installation: succeeded, Loading: succeeded}, resultOf(t, execution, "runtime"))
	require.Equal(t, domain.PluginResult{Slug: "tools", Installation: succeeded, Loading: succeeded, LoadedVersion: "9.9.9"}, resultOf(t, execution, "tools"))
}

// When a later report for a Plugin arrives, the execution replaces that
// Plugin's outcome (Plugin result replacement).
func TestALaterReportReplacesThatPluginsOutcome(t *testing.T) {
	execution := preparedPluginExecution(t)
	require.True(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"}, domain.LoadedPlugin{Slug: "tools", Version: "1.0.0"}))
	require.Equal(t, "1.0.0", resultOf(t, execution, "tools").LoadedVersion)

	require.True(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "tools", Version: "2.0.0"}))
	require.Equal(t, succeeded, loadingOf(t, execution, "tools"))
	require.Equal(t, "2.0.0", resultOf(t, execution, "tools").LoadedVersion)
	require.Empty(t, resultOf(t, execution, "runtime").LoadedVersion, "a Plugin no longer loaded has no loaded version")
	require.Equal(t, notLoaded, loadingOf(t, execution, "runtime"), "the later inventory replaces every selected Plugin's loading")

	require.False(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "tools", Version: "2.0.0"}), "the same outcomes reported again change nothing")
}

// A report from a process other than its launched one describes another
// start, and changes nothing.
func TestAReportAboutAnotherStartIsIgnored(t *testing.T) {
	execution := preparedPluginExecution(t)
	before := execution.PluginResults
	changed, err := execution.RecordPreparation(domain.PreparationReport{ProcessID: "process-of-an-earlier-start", Loaded: []domain.LoadedPlugin{{Slug: "runtime", Version: "1.0.0"}}})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, before, execution.PluginResults)

	unlaunched := boundPluginExecution(t)
	require.False(t, reportLoaded(t, unlaunched, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"}), "an execution that launched nothing has no process to report")
	require.Empty(t, unlaunched.PluginResults)
}

// Only the Plugins the run selected have outcomes.
func TestAPluginOutsideTheSelectionIsIgnored(t *testing.T) {
	execution := boundPluginExecution(t)
	_, err := execution.RecordPreparation(domain.PreparationReport{Installations: []domain.PluginInstallation{
		{Slug: "runtime", Installed: true}, {Slug: "unselected", Installed: true},
	}})
	require.NoError(t, err)
	require.Equal(t, []domain.PluginResult{{Slug: "runtime", Installation: succeeded}}, execution.PluginResults)

	_, err = execution.RecordLaunch("process-1")
	require.NoError(t, err)
	reportLoaded(t, execution, domain.LoadedPlugin{Slug: "unselected", Version: "9.0.0"})
	for _, result := range execution.PluginResults {
		require.NotEqual(t, "unselected", result.Slug)
	}
}

// Each start's execution begins with no outcomes, whatever an earlier start's
// run recorded.
func TestANewStartBeginsWithNoOutcomes(t *testing.T) {
	work := pluginWork(t)
	require.Empty(t, beginOf(t, work, domain.RealizationCloud).PluginResults)

	failed := preparedPluginExecution(t)
	reportLoaded(t, failed)
	require.Equal(t, notLoaded, loadingOf(t, failed, "runtime"))
	next := beginOf(t, work, domain.RealizationCloud)
	require.Empty(t, next.PluginResults, "an earlier start's failure is not the next start's outcome")
}

// A report is either the preparation's installation report or a launched
// process's loading report, each naming a Plugin once.
func TestAMalformedPreparationReportIsRefused(t *testing.T) {
	execution := preparedPluginExecution(t)
	for name, report := range map[string]domain.PreparationReport{
		"installation from a process": {ProcessID: "process-1", Installations: []domain.PluginInstallation{{Slug: "runtime", Installed: true}}},
		"loading without a process":   {Loaded: []domain.LoadedPlugin{{Slug: "runtime", Version: "1.0.0"}}},
		"a Plugin loaded twice":       {ProcessID: "process-1", Loaded: []domain.LoadedPlugin{{Slug: "runtime"}, {Slug: "runtime"}}},
		"a Plugin installed twice":    {Installations: []domain.PluginInstallation{{Slug: "runtime"}, {Slug: "runtime"}}},
		"a Plugin without a slug":     {ProcessID: "process-1", Loaded: []domain.LoadedPlugin{{Slug: " "}}},
	} {
		_, err := execution.RecordPreparation(report)
		require.ErrorIs(t, err, domain.ErrInvalidExecution, name)
	}
}

// A retired execution records no further outcome.
func TestARetiredExecutionRecordsNoPluginOutcome(t *testing.T) {
	execution := preparedPluginExecution(t)
	retired, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: execution.WorkID, StartAdmittedAt: execution.StartAdmittedAt, Cause: domain.RetiredUnreached}, time.Now())
	require.NoError(t, err)
	require.True(t, retired)
	require.False(t, reportLoaded(t, execution, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"}))
}

//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
)

// beginPluginRun saves a new execution of the Work that selected runtime
// 1.0.0 and tools 2.0.0, binds it and records its preparation's report.
func beginPluginRun(t *testing.T, repository *AgentExecutionRepository, workID string, admittedAt time.Time, installations ...domain.PluginInstallation) *domain.AgentExecution {
	t.Helper()
	launch := request()
	launch.WorkID = workID
	launch.Capabilities.Plugins = []domain.SelectedPlugin{{Release: fixedRelease("tools", "2.0.0")}, {Release: fixedRelease("runtime", "1.0.0")}}
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: domain.WorkStartAdmitted{WorkID: workID, StartAdmittedAt: admittedAt, DeskID: "desk-" + workID}, Launch: launch, Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.True(t, commandtest.Submit(run))
	_, err = run.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
	require.NoError(t, err)
	prepared, err := run.RecordPreparation(domain.PreparationReport{Installations: installations})
	require.NoError(t, err)
	require.True(t, prepared)
	_, err = run.RecordLaunch("process-" + run.ID)
	require.NoError(t, err)
	require.NoError(t, repository.Save(context.Background(), run))
	stored, err := repository.Get(context.Background(), run.ID)
	require.NoError(t, err)
	return stored
}

func fixedRelease(slug, version string) domain.PluginRelease {
	return domain.PluginRelease{CatalogName: "talgent", Slug: slug, Source: "fixture", Version: version, RetrievalRef: "archive:" + slug, NativeEntry: []byte(`{"name":"` + slug + `","version":"` + version + `"}`)}
}

// reportLoads records the inventory the run's launched process reports and
// saves it.
func reportLoads(t *testing.T, repository *AgentExecutionRepository, run *domain.AgentExecution, loaded ...domain.LoadedPlugin) {
	t.Helper()
	changed, err := run.RecordPreparation(domain.PreparationReport{ProcessID: run.Process.ID, Loaded: loaded})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, repository.Save(context.Background(), run))
}

// An execution's Plugin outcomes are saved with it under its version: a
// later report replaces a Plugin's outcome, installation and loading stay
// independent, and a save that loses its version writes no outcome.
func TestAgentExecutionRepositoryKeepsPluginOutcomesWithTheRun(t *testing.T) {
	ctx := context.Background()
	repository := NewAgentExecutionRepository(executionDatabase(t), time.Now)
	run := beginPluginRun(t, repository, uuid.NewString(), time.Now(),
		domain.PluginInstallation{Slug: "runtime", Reason: "installation_failed"}, domain.PluginInstallation{Slug: "tools", Installed: true})
	require.Equal(t, []domain.PluginResult{
		{Slug: "runtime", Installation: domain.PluginOutcome{Reported: true, Reason: "installation_failed"}},
		{Slug: "tools", Installation: domain.PluginOutcome{Reported: true, Succeeded: true}},
	}, run.PluginResults)

	reportLoads(t, repository, run, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"})
	run, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, []domain.PluginResult{
		{Slug: "runtime", Installation: domain.PluginOutcome{Reported: true, Reason: "installation_failed"}, Loading: domain.PluginOutcome{Reported: true, Succeeded: true}, LoadedVersion: "1.0.0"},
		{Slug: "tools", Installation: domain.PluginOutcome{Reported: true, Succeeded: true}, Loading: domain.PluginOutcome{Reported: true, Reason: domain.PluginNotLoaded}},
	}, run.PluginResults, "loading leaves the installation outcome as reported")

	reportLoads(t, repository, run, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.1"}, domain.LoadedPlugin{Slug: "tools", Version: "2.0.0"})
	latest, err := repository.Latest(ctx, run.WorkID)
	require.NoError(t, err)
	require.Equal(t, domain.PluginOutcome{Reported: true, Succeeded: true}, latest.PluginResults[0].Loading)
	require.Equal(t, "1.0.1", latest.PluginResults[0].LoadedVersion, "a later report replaces the Plugin's outcome, at the version it loaded")
	require.Equal(t, domain.PluginOutcome{Reported: true, Succeeded: true}, latest.PluginResults[1].Loading)
	require.Equal(t, "2.0.0", latest.PluginResults[1].LoadedVersion)

	// run was saved already: its version is stale, and its report is refused
	// whole, its outcomes included.
	changed, err := run.RecordPreparation(domain.PreparationReport{ProcessID: run.Process.ID})
	require.NoError(t, err)
	require.True(t, changed)
	require.ErrorIs(t, repository.Save(ctx, run), domain.ErrConcurrentModification)
	after, err := repository.Get(ctx, run.ID)
	require.NoError(t, err)
	require.Equal(t, latest.PluginResults, after.PluginResults)
}

// The Work's Plugin results are those its most recently begun execution
// recorded: a new start begins with none, and an earlier start's failure is
// not current.
func TestCurrentPluginResultsReadTheLatestExecutionOnly(t *testing.T) {
	ctx := context.Background()
	db := executionDatabase(t)
	at := time.UnixMilli(time.Now().UnixMilli()).UTC()
	repository := NewAgentExecutionRepository(db, func() time.Time { return at })
	results := infrastructure.NewExecutionQueryRepository(db)
	workID := uuid.NewString()

	none, err := results.ListCurrentPluginResults(ctx, workID)
	require.NoError(t, err)
	require.Empty(t, none, "a Work without an execution has no outcomes")

	earlier := beginPluginRun(t, repository, workID, at, domain.PluginInstallation{Slug: "runtime", Reason: "installation_failed"}, domain.PluginInstallation{Slug: "tools", Installed: true})
	other := beginPluginRun(t, repository, uuid.NewString(), at, domain.PluginInstallation{Slug: "runtime", Installed: true})
	current, err := results.ListCurrentPluginResults(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, []query.PluginResult{
		{Slug: "runtime", InstallationReported: true, InstallationReason: "installation_failed", UpdatedAt: at},
		{Slug: "tools", InstallationReported: true, Installed: true, UpdatedAt: at},
	}, current, "another Work's run is not this Work's")
	require.NotEqual(t, other.WorkID, workID)

	at = at.Add(time.Minute)
	retire(t, repository, earlier, at, false)
	next := beginPluginRun(t, repository, workID, at)
	current, err = results.ListCurrentPluginResults(ctx, workID)
	require.NoError(t, err)
	require.Empty(t, current, "a new start begins with no outcomes")

	at = at.Add(time.Minute)
	reportLoads(t, repository, next, domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"})
	current, err = results.ListCurrentPluginResults(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, []query.PluginResult{
		{Slug: "runtime", LoadedVersion: "1.0.0", LoadingReported: true, Loaded: true, UpdatedAt: at},
		{Slug: "tools", LoadingReported: true, LoadingReason: domain.PluginNotLoaded, UpdatedAt: at},
	}, current, "only the current run's outcomes are read")
}

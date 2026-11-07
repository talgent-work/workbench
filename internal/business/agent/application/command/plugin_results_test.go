package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// pluginWorld is a Work that selected runtime 1.0.0, tools 2.0.0 and missing
// 3.0.0, whose execution's environment is bound and running.
func pluginWorld(t *testing.T) *world {
	t.Helper()
	work := startingSession(t)
	work.CapabilitySnapshot.Plugins = []domain.SelectedPlugin{
		{Release: domain.PluginRelease{Slug: "runtime", Version: "1.0.0", Source: "official"}},
		{Release: domain.PluginRelease{Slug: "tools", Version: "2.0.0", Source: "community"}},
		{Release: domain.PluginRelease{Slug: "missing", Version: "3.0.0", Source: "community"}},
	}
	w := newWorld(t, work)
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, execution.RecordSubmission())
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	})
	w.environment.Observation = running("environment")
	return w
}

func pluginLoads(w *world, processID string, loaded ...domain.LoadedPlugin) command.ExecutionFeedback {
	return command.ExecutionFeedback{Kind: command.PluginLoadsObserved, WorkID: w.work().ID, ProcessID: processID, ObservationSequence: 1, LoadedPlugins: loaded}
}

// The preparation's report of each Plugin's installation is recorded on the
// execution with the end of its preparation, before the Harness launches.
func TestPreparationRecordsPluginInstallationOnTheExecution(t *testing.T) {
	w := pluginWorld(t)
	w.environment.Installations = []domain.PluginInstallation{
		{Slug: "runtime", Reason: "installation_failed"}, {Slug: "tools", Installed: true}, {Slug: "missing", Reason: "preparation_failed"},
	}
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	execution := w.execution(t)
	require.True(t, execution.Process.PreparationEnded)
	require.Equal(t, []domain.PluginResult{
		{Slug: "runtime", Installation: domain.PluginOutcome{Reported: true, Reason: "installation_failed"}},
		{Slug: "tools", Installation: domain.PluginOutcome{Reported: true, Succeeded: true}},
		{Slug: "missing", Installation: domain.PluginOutcome{Reported: true, Reason: "preparation_failed"}},
	}, execution.PluginResults)
	require.Equal(t, 1, w.environment.Launched)
}

// The launched process's complete inventory is matched against the run's
// fixed releases on its execution; the Work is not changed.
func TestPluginLoadFeedbackRecordsLoadingOnTheCurrentExecution(t *testing.T) {
	ctx := context.Background()
	w := pluginWorld(t)
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	saves := w.works.saves
	require.NoError(t, w.feedback().Handle(ctx, pluginLoads(w, "process",
		domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"}, domain.LoadedPlugin{Slug: "tools", Version: "1.0.0"}, domain.LoadedPlugin{Slug: "unselected", Version: "9.0.0"})))
	require.Equal(t, []domain.PluginResult{
		{Slug: "runtime", Loading: domain.PluginOutcome{Reported: true, Succeeded: true}},
		{Slug: "tools", Loading: domain.PluginOutcome{Reported: true, Reason: domain.PluginVersionMismatch}},
		{Slug: "missing", Loading: domain.PluginOutcome{Reported: true, Reason: domain.PluginNotLoaded}},
	}, w.execution(t).PluginResults, "installation and loading remain independent; undeclared Plugins are omitted")
	require.Equal(t, saves, w.works.saves, "loading feedback is no Work lifecycle transition")

	require.NoError(t, w.feedback().Handle(ctx, pluginLoads(w, "process")))
	for _, result := range w.execution(t).PluginResults {
		require.Equal(t, domain.PluginOutcome{Reported: true, Reason: domain.PluginNotLoaded}, result.Loading, "an empty complete inventory replaces earlier loading")
	}
}

// Feedback from another process than the execution's launched one, such as
// an earlier start's, changes no outcome.
func TestPluginLoadFeedbackAboutAnotherProcessChangesNothing(t *testing.T) {
	ctx := context.Background()
	w := pluginWorld(t)
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	saves := w.executions.Saves
	require.NoError(t, w.feedback().Handle(ctx, pluginLoads(w, "earlier", domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"})))
	require.Equal(t, saves, w.executions.Saves)
	require.Empty(t, w.execution(t).PluginResults)
}

// A malformed inventory is refused before anything is written.
func TestPluginLoadFeedbackRejectsInvalidInventoryBeforeWriting(t *testing.T) {
	w := pluginWorld(t)
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	saves := w.executions.Saves
	for name, loaded := range map[string][]domain.LoadedPlugin{
		"blank slug":          {{Slug: " "}},
		"long slug":           {{Slug: strings.Repeat("x", 129)}},
		"long version":        {{Slug: "runtime", Version: strings.Repeat("x", 257)}},
		"duplicate":           {{Slug: "runtime"}, {Slug: "runtime"}},
		"oversized inventory": make([]domain.LoadedPlugin, 4097),
	} {
		require.ErrorIs(t, w.feedback().Handle(context.Background(), pluginLoads(w, "process", loaded...)), domain.ErrInvalidWork, name)
	}
	blankWork := pluginLoads(w, "process")
	blankWork.WorkID = " "
	require.ErrorIs(t, w.feedback().Handle(context.Background(), blankWork), domain.ErrInvalidWork, "blank work")
	require.Equal(t, saves, w.executions.Saves)
}

// A failure to save the outcomes is returned, so the report is delivered
// again.
func TestPluginLoadFeedbackPropagatesStorageFailure(t *testing.T) {
	w := pluginWorld(t)
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	unavailable := errors.New("execution storage unavailable")
	w.executions.SaveErr = unavailable
	require.ErrorIs(t, w.feedback().Handle(context.Background(), pluginLoads(w, "process", domain.LoadedPlugin{Slug: "runtime", Version: "1.0.0"})), unavailable)
	w.executions.SaveErr = nil
	require.Empty(t, w.execution(t).PluginResults)
}

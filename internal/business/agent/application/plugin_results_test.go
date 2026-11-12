package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// currentRunOutcomes stands in for the execution query repository's read of
// the current run's Plugin outcomes.
type currentRunOutcomes struct {
	query.ExecutionQueryRepository
	outcomes []query.PluginResult
	workID   string
	err      error
}

func (r *currentRunOutcomes) ListCurrentPluginResults(_ context.Context, workID string) ([]query.PluginResult, error) {
	r.workID = workID
	return r.outcomes, r.err
}

// The Work's selected releases are listed with the current run's outcomes;
// an outcome of a Plugin the Work no longer selects is omitted.
func TestPluginResultsListTheSelectionWithTheCurrentRunsOutcomes(t *testing.T) {
	app, works, auth, _ := thinkingApplication()
	works.work.CapabilitySnapshot.Plugins = []domain.SelectedPlugin{
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "runtime", Version: "1.0.0", Source: "official"}},
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "tools", Version: "2.0.0", Source: "community"}},
		{Release: domain.PluginRelease{CatalogName: "talgent", Slug: "missing", Version: "3.0.0", Source: "community"}},
	}
	outcomes := &currentRunOutcomes{outcomes: []query.PluginResult{
		{Slug: "runtime", InstallationReported: true, InstallationReason: "installation_failed", LoadingReported: true, Loaded: true},
		{Slug: "tools", InstallationReported: true, Installed: true, LoadingReported: true, LoadingReason: "version_mismatch"},
		{Slug: "unselected", Loaded: true},
	}}
	app.ExecutionQueries = outcomes
	results, err := app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "reader", WorkID: "work"})
	require.NoError(t, err)
	require.Equal(t, "reader", auth.subject)
	require.Equal(t, "work", outcomes.workID)
	require.Equal(t, []query.PluginResult{
		{Slug: "runtime", ReleaseVersion: "1.0.0", Source: "official", InstallationReported: true, InstallationReason: "installation_failed", LoadingReported: true, Loaded: true},
		{Slug: "tools", ReleaseVersion: "2.0.0", Source: "community", InstallationReported: true, Installed: true, LoadingReported: true, LoadingReason: "version_mismatch"},
		{Slug: "missing", ReleaseVersion: "3.0.0", Source: "community"},
	}, results, "installation and loading remain independent; undeclared plugins are omitted")
	require.Zero(t, works.saves)
}

func TestPluginResultsEnforceAccessAndPropagateStorageFailure(t *testing.T) {
	app, _, auth, _ := thinkingApplication()
	_, err := app.GetWorkPluginResults(t.Context(), application.ControlWork{WorkID: " "})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	auth.err = access.ErrAccessDenied
	outcomes := &currentRunOutcomes{}
	app.ExecutionQueries = outcomes
	_, err = app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "outsider", WorkID: "work"})
	require.ErrorIs(t, err, access.ErrAccessDenied, "authorization precedes reading Plugin outcomes")
	require.Empty(t, outcomes.workID)
	auth.err = nil
	unavailable := errors.New("execution query storage unavailable")
	app.ExecutionQueries = &currentRunOutcomes{err: unavailable}
	_, err = app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "reader", WorkID: "work"})
	require.ErrorIs(t, err, unavailable)
}

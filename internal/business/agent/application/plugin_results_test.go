package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type pluginProjectionStore struct {
	observations []query.PluginResult
	loads        []application.PluginLoadResult
	workID       string
	writes       int
	err          error
}

func (s *pluginProjectionStore) Read(context.Context, string) ([]query.PluginResult, error) {
	return s.observations, s.err
}
func (s *pluginProjectionStore) RecordLoads(_ context.Context, workID string, loads []application.PluginLoadResult) error {
	s.workID, s.loads = workID, loads
	s.writes++
	return s.err
}

func TestPluginInventoryMatchesFixedReleasesWithoutChangingWork(t *testing.T) {
	// The repository and projection sink are external boundaries. All inventory
	// validation, release matching and public assembly use the real Application.
	app, works, auth, _ := thinkingApplication()
	works.work.CapabilitySnapshot.Plugins = []domain.SelectedPlugin{
		{Release: domain.PluginRelease{Slug: "runtime", Version: "1.0.0", Source: "official"}},
		{Release: domain.PluginRelease{Slug: "tools", Version: "2.0.0", Source: "community"}},
		{Release: domain.PluginRelease{Slug: "missing", Version: "3.0.0", Source: "community"}},
	}
	projection := &pluginProjectionStore{}
	app.PluginResults = projection
	require.NoError(t, app.RecordPluginLoads(t.Context(), "work", []application.PluginLoad{
		{Slug: "runtime", Version: "1.0.0"}, {Slug: "tools", Version: "1.0.0"}, {Slug: "unselected", Version: "9.0.0"},
	}))
	require.Equal(t, "work", projection.workID)
	require.Equal(t, []application.PluginLoadResult{
		{Slug: "runtime", Loaded: true}, {Slug: "tools", Reason: "version_mismatch"}, {Slug: "missing", Reason: "not_loaded"},
	}, projection.loads)
	require.Zero(t, works.saves, "load feedback is a projection, not a Work lifecycle transition")

	projection.observations = []query.PluginResult{
		{Slug: "runtime", InstallationObserved: true, InstallationReason: "installation_failed", LoadObserved: true, Loaded: true},
		{Slug: "tools", InstallationObserved: true, Installed: true, LoadObserved: true, LoadReason: "version_mismatch"},
		{Slug: "unselected", Loaded: true},
	}
	results, err := app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "reader", WorkID: "work"})
	require.NoError(t, err)
	require.NotEmpty(t, auth.subject)
	require.Equal(t, []query.PluginResult{
		{Slug: "runtime", ReleaseVersion: "1.0.0", Source: "official", InstallationObserved: true, InstallationReason: "installation_failed", LoadObserved: true, Loaded: true},
		{Slug: "tools", ReleaseVersion: "2.0.0", Source: "community", InstallationObserved: true, Installed: true, LoadObserved: true, LoadReason: "version_mismatch"},
		{Slug: "missing", ReleaseVersion: "3.0.0", Source: "community"},
	}, results, "installation and loading remain independent; undeclared plugins are omitted")

	require.NoError(t, app.RecordPluginLoads(t.Context(), "work", nil))
	for _, result := range projection.loads {
		require.False(t, result.Loaded)
		require.Equal(t, "not_loaded", result.Reason, "empty complete inventory replaces previous loading facts")
	}
}

func TestPluginInventoryRejectsInvalidInputBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name, workID string
		plugins      []application.PluginLoad
	}{
		{name: "blank work", workID: " "},
		{name: "blank slug", workID: "work", plugins: []application.PluginLoad{{Slug: " "}}},
		{name: "long slug", workID: "work", plugins: []application.PluginLoad{{Slug: strings.Repeat("x", 129)}}},
		{name: "long version", workID: "work", plugins: []application.PluginLoad{{Slug: "runtime", Version: strings.Repeat("x", 257)}}},
		{name: "duplicate", workID: "work", plugins: []application.PluginLoad{{Slug: "runtime"}, {Slug: "runtime"}}},
		{name: "oversized inventory", workID: "work", plugins: make([]application.PluginLoad, 4097)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			projection := &pluginProjectionStore{}
			app := &application.Application{PluginResults: projection}
			require.ErrorIs(t, app.RecordPluginLoads(t.Context(), tc.workID, tc.plugins), domain.ErrInvalidWork)
			require.Zero(t, projection.writes)
		})
	}
}

func TestPluginResultsEnforceAccessAndPropagateStorageFailure(t *testing.T) {
	app, _, auth, _ := thinkingApplication()
	_, err := app.GetWorkPluginResults(t.Context(), application.ControlWork{WorkID: " "})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	auth.err = access.ErrAccessDenied
	_, err = app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "outsider", WorkID: "work"})
	require.ErrorIs(t, err, access.ErrAccessDenied, "authorization precedes reading plugin projections")
	auth.err = nil
	unavailable := errors.New("projection storage unavailable")
	app.PluginResults = &pluginProjectionStore{err: unavailable}
	_, err = app.GetWorkPluginResults(t.Context(), application.ControlWork{ActorUserID: "reader", WorkID: "work"})
	require.ErrorIs(t, err, unavailable)
	require.ErrorIs(t, app.RecordPluginLoads(t.Context(), "work", nil), unavailable)
}

//go:build integration

package infrastructure_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func TestCatalogPartialFailureClassificationAndDatabaseQueries(t *testing.T) {
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	source := application.CatalogSource{Name: "owned", Owned: true}
	a, b := catalogRelease("owned", "alpha", "1"), catalogRelease("owned", "beta", "1")
	require.NoError(t, store.Publish(t.Context(), []infrastructure.SourceReleases{{Source: source, Releases: []application.PluginRelease{a, b}}}))
	a = catalogRelease("owned", "alpha", "2")
	a.Agents = []application.AgentComponent{{Name: "alpha:review"}}
	a.MCPServers = []application.MCPComponent{{Name: "api", Definition: []byte(`{"command":"node","args":["public.cjs"]}`)}}
	b.Description = "same identity with changed content"
	first := catalogRelease("owned", "first-failure", "")
	oversized := catalogRelease("owned", "oversized-version", strings.Repeat("v", 256))
	err = store.Publish(t.Context(), []infrastructure.SourceReleases{{Source: source, Releases: []application.PluginRelease{a, b, first, oversized}}})
	require.ErrorIs(t, err, domain.ErrReleaseChanged)
	require.ErrorIs(t, err, domain.ErrInvalidRelease)
	selected, err := store.Get(t.Context(), "alpha")
	require.NoError(t, err)
	require.Equal(t, "2", selected.Version)
	selected, err = store.Get(t.Context(), "beta")
	require.NoError(t, err)
	require.Equal(t, "1", selected.Version)
	require.Empty(t, selected.Description)
	_, err = store.Get(t.Context(), "first-failure")
	require.ErrorIs(t, err, application.ErrReleaseNotFound)
	_, err = store.Get(t.Context(), "oversized-version")
	require.ErrorIs(t, err, application.ErrReleaseNotFound, "invalid publication identity cannot abort other Plugin updates at the SQL boundary")
	for _, kind := range []string{"skill", "agent", "mcp"} {
		page, err := store.List(t.Context(), "", 1, kind)
		require.NoError(t, err)
		require.Len(t, page.Releases, 1)
		require.Equal(t, "alpha", page.Releases[0].Slug)
		if kind == "skill" {
			require.Equal(t, "alpha", page.NextCursor)
		} else {
			require.Empty(t, page.NextCursor)
		}
	}
	_, err = store.List(t.Context(), "", 10, "unknown")
	require.ErrorIs(t, err, application.ErrInvalidQuery)
	err = store.Publish(t.Context(), []infrastructure.SourceReleases{{Source: source, Failures: map[string]error{"alpha": errors.New("fetch failed"), "new": errors.New("first failure")}, Releases: []application.PluginRelease{catalogRelease("owned", "beta", "2")}}})
	require.Error(t, err)
	selected, err = store.Get(t.Context(), "alpha")
	require.NoError(t, err)
	require.Equal(t, "2", selected.Version)
	selected, err = store.Get(t.Context(), "beta")
	require.NoError(t, err)
	require.Equal(t, "2", selected.Version)
	_, err = store.Get(t.Context(), "new")
	require.ErrorIs(t, err, application.ErrReleaseNotFound)
}

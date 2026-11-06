//go:build integration

package infrastructure_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func catalogRelease(source, slug, version string) application.PluginRelease {
	entry, _ := json.Marshal(map[string]any{"name": slug, "version": version, "source": map[string]any{"source": "archive", "url": "https://example.com/" + source + "/" + slug + "/" + version + ".zip"}})
	return application.PluginRelease{Slug: slug, Source: source, Version: version, RetrievalRef: source + "/" + slug + "/" + version, NativeEntry: entry, Skills: []application.SkillComponent{{Name: slug + ":" + source}}}
}

func TestCatalogSelectsWholeReleaseAndOwnedSourcePrecedesPriority(t *testing.T) {
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	sources := []infrastructure.SourceReleases{
		{Source: application.CatalogSource{Name: "external", Priority: 999}, Releases: []application.PluginRelease{catalogRelease("external", "shared", "9.0.0"), catalogRelease("external", "zeta", "1.0.0")}},
		{Source: application.CatalogSource{Name: "owned", Owned: true}, Releases: []application.PluginRelease{catalogRelease("owned", "shared", "1.0.0")}},
		{Source: application.CatalogSource{Name: "secondary", Priority: 1}, Releases: []application.PluginRelease{catalogRelease("secondary", "zeta", "3.0.0")}},
	}
	require.NoError(t, store.Publish(t.Context(), sources))
	selected, err := store.Get(t.Context(), "shared")
	require.NoError(t, err)
	require.Equal(t, "owned", selected.Source)
	require.Equal(t, "1.0.0", selected.Version)
	require.Equal(t, "shared:owned", selected.Skills[0].Name)
	page, err := store.List(t.Context(), "", 1, "")
	require.NoError(t, err)
	require.Equal(t, "shared", page.NextCursor)
	last, err := store.List(t.Context(), page.NextCursor, 1, "")
	require.NoError(t, err)
	require.Equal(t, "external", last.Releases[0].Source)
	require.Empty(t, last.NextCursor)
	encoded, err := store.Marketplace(t.Context())
	require.NoError(t, err)
	var index struct {
		Name    string
		Plugins []struct {
			Name    string
			Version string
			Source  struct{ URL string }
		}
	}
	require.NoError(t, json.Unmarshal(encoded, &index))
	require.Equal(t, "talgent", index.Name)
	require.Equal(t, "https://example.com/owned/shared/1.0.0.zip", index.Plugins[0].Source.URL)
	require.Equal(t, "https://example.com/external/zeta/1.0.0.zip", index.Plugins[1].Source.URL)
}

//go:build integration

package infrastructure_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
)

// A source dropped from configuration takes its releases, catalog entries and
// the archives only it referenced with it. An archive another configured
// source still references stays served.
func TestCatalogSynchronizationRetiresRemovedSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	private, kept := t.TempDir(), t.TempDir()
	shared := `{"name":"shared-plugin","version":"1.0.0"}`
	writeMetadata(t, private, "plugins/private-plugin/.claude-plugin/plugin.json", `{"name":"private-plugin","version":"1.0.0"}`)
	writeMetadata(t, private, "plugins/shared-plugin/.claude-plugin/plugin.json", shared)
	writeCatalog(t, private, "private-catalog", []map[string]any{{"name": "private-plugin", "source": "./plugins/private-plugin"}, {"name": "shared-plugin", "source": "./plugins/shared-plugin"}})
	writeMetadata(t, kept, "plugins/kept-plugin/.claude-plugin/plugin.json", `{"name":"kept-plugin","version":"1.0.0"}`)
	writeMetadata(t, kept, "plugins/shared-plugin/.claude-plugin/plugin.json", shared)
	writeCatalog(t, kept, "kept-catalog", []map[string]any{{"name": "kept-plugin", "source": "./plugins/kept-plugin"}, {"name": "shared-plugin", "source": "./plugins/shared-plugin"}})
	content := t.TempDir()
	db := catalogDatabase(t)
	store, err := infrastructure.NewCatalogStore(db, content, "talgent", "Talgent", slog.Default())
	require.NoError(t, err)
	handler := marketplacehttp.NewCatalogHandler(application.NewApplication(nil, nil, store, nil, nil, application.ConnectorRepositories{}), slog.Default())
	synchronize := func(sources map[string]infrastructure.CatalogSource) {
		t.Helper()
		synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 30,
			ArchiveBaseURL: "https://catalog.example.test/marketplace/archives", Sources: sources}, store)
		require.NoError(t, err)
		require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	}
	archiveOf := func(slug string) string {
		t.Helper()
		release, err := store.Get(ctx, slug)
		require.NoError(t, err)
		var entry struct{ Source struct{ SHA256 string } }
		require.NoError(t, json.Unmarshal(release.NativeEntry, &entry))
		return entry.Source.SHA256
	}
	served := func(digest string) int {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, marketplacehttp.ContentPath+"archives/"+digest+".zip", nil))
		return recorder.Code
	}
	live := func(table, source string) int {
		t.Helper()
		count, err := db.Context(ctx).Table(table).Where("source = ? AND deleted_at = 0", source).Count()
		require.NoError(t, err)
		return int(count)
	}
	both := map[string]infrastructure.CatalogSource{"private": {URI: private, Owned: true, Priority: 10}, "kept": {URI: kept, Owned: true}}
	onlyKept := map[string]infrastructure.CatalogSource{"kept": {URI: kept, Owned: true}}

	synchronize(both)
	privateArchive, sharedArchive, keptArchive := archiveOf("private-plugin"), archiveOf("shared-plugin"), archiveOf("kept-plugin")
	require.Equal(t, http.StatusOK, served(privateArchive))

	synchronize(onlyKept)
	_, err = store.Get(ctx, "private-plugin")
	require.ErrorIs(t, err, application.ErrReleaseNotFound)
	require.Equal(t, http.StatusNotFound, served(privateArchive), "a removed source's archive is no longer served")
	require.NoFileExists(t, filepath.Join(content, "archives", privateArchive+".zip"))
	require.Equal(t, sharedArchive, archiveOf("shared-plugin"), "the kept source publishes the identical package")
	require.Equal(t, http.StatusOK, served(sharedArchive), "an archive a configured source references stays")
	require.Equal(t, http.StatusOK, served(keptArchive))
	require.Zero(t, live("marketplace_plugin_release", "private"))
	require.Zero(t, live("marketplace_catalog_entry", "private"))
	require.Equal(t, 2, live("marketplace_plugin_release", "kept"))

	// Retiring again changes nothing.
	synchronize(onlyKept)
	require.Equal(t, http.StatusOK, served(sharedArchive))
	require.Equal(t, http.StatusOK, served(keptArchive))
	require.Equal(t, 2, live("marketplace_plugin_release", "kept"))

	// Configuring the source again publishes it again.
	synchronize(both)
	require.Equal(t, privateArchive, archiveOf("private-plugin"))
	require.Equal(t, http.StatusOK, served(privateArchive))
	require.Equal(t, 2, live("marketplace_plugin_release", "private"))
}

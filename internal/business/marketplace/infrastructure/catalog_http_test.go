//go:build integration

package infrastructure_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
)

func TestCatalogHTTPReadinessCachingAndContentIsolation(t *testing.T) {
	root := t.TempDir()
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), root, "talgent", "Talgent")
	require.NoError(t, err)
	server := httptest.NewServer(marketplacehttp.NewCatalogHandler(application.NewApplication(nil, nil, store, nil, nil, application.ConnectorRepositories{}), slog.Default()))
	t.Cleanup(server.Close)
	get := func(method, path, etag string) (*http.Response, []byte) {
		t.Helper()
		request, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, nil)
		require.NoError(t, err)
		request.Header.Set("If-None-Match", etag)
		response, err := server.Client().Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		return response, body
	}
	response, _ := get(http.MethodGet, "/marketplace/marketplace.json", "")
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	sources := []infrastructure.SourceReleases{{Source: application.CatalogSource{Name: "owned", Owned: true}, Releases: []application.PluginRelease{catalogRelease("owned", "reviewer", "1.0.0")}}}
	require.NoError(t, store.Publish(t.Context(), sources))
	response, body := get(http.MethodGet, "/marketplace/marketplace.json", "")
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, string(body), `"name":"reviewer"`)
	require.Equal(t, "no-cache", response.Header.Get("Cache-Control"))
	etag := response.Header.Get("ETag")
	require.NotEmpty(t, etag)
	sources[0].Releases[0] = catalogRelease("owned", "reviewer", "2.0.0")
	require.NoError(t, store.Publish(t.Context(), sources))
	response, body = get(http.MethodGet, "/marketplace/marketplace.json", etag)
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, string(body), `"version":"2.0.0"`)
	require.NotEqual(t, etag, response.Header.Get("ETag"))
	response, _ = get(http.MethodPost, "/marketplace/marketplace.json", "")
	require.Equal(t, http.StatusMethodNotAllowed, response.StatusCode)
	for _, path := range []string{"/marketplace/catalog.json", "/marketplace/archives/not-a-digest.zip", "/marketplace/archives/" + strings.Repeat("0", 64) + ".zip", "/marketplace/archives/../catalog.json.zip"} {
		response, _ := get(http.MethodGet, path, "")
		require.Equal(t, http.StatusNotFound, response.StatusCode, path)
	}
	// A guessed digest cannot follow a symlink out of the owned archive namespace.
	require.NoError(t, os.Mkdir(filepath.Join(root, "archives"), 0o700))
	outside := filepath.Join(t.TempDir(), "private")
	require.NoError(t, os.WriteFile(outside, []byte("private secret"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(root, "archives", strings.Repeat("1", 64)+".zip")))
	response, body = get(http.MethodGet, "/marketplace/archives/"+strings.Repeat("1", 64)+".zip", "")
	require.NotEqual(t, http.StatusOK, response.StatusCode)
	require.NotContains(t, string(body), "private secret")
	require.NotContains(t, string(body), outside)
}

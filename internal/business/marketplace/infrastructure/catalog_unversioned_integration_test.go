//go:build integration

package infrastructure_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

// lockedBuffer collects log output written by concurrent synchronization workers.
type lockedBuffer struct {
	mu   sync.Mutex
	body bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.body.Write(p)
}
func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.body.String()
}

// unversionedGitServer serves the git repositories under its root over HTTPS
// and counts the requests it answers.
type unversionedGitServer struct {
	root, url, certificate string
	requests               *atomic.Int32
}

func newUnversionedGitServer(t *testing.T, extra http.Handler) unversionedGitServer {
	t.Helper()
	root := t.TempDir()
	requests := &atomic.Int32{}
	backend := &cgi.Handler{Path: filepath.Join(gitFixture(t, root, "--exec-path"), "git-http-backend"), Dir: root, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1"}}
	server := newCatalogHTTPSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if extra != nil && filepath.Ext(r.URL.Path) == ".zip" {
			extra.ServeHTTP(w, r)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	return unversionedGitServer{root: root, url: server.URL, certificate: certificate, requests: requests}
}

// commit records the work tree and publishes it to the bare repository the
// server serves under name, returning the commit.
func (s unversionedGitServer) commit(t *testing.T, work, name string) string {
	t.Helper()
	gitFixture(t, work, "add", ".")
	gitFixture(t, work, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "change")
	bare := filepath.Join(s.root, name)
	if _, err := os.Stat(bare); os.IsNotExist(err) {
		gitFixture(t, s.root, "init", "--quiet", "--bare", name)
	}
	gitFixture(t, work, "push", "--quiet", bare, "HEAD:refs/heads/main")
	gitFixture(t, bare, "symbolic-ref", "HEAD", "refs/heads/main")
	return gitFixture(t, work, "rev-parse", "HEAD")
}

func nativeVersion(t *testing.T, release application.PluginRelease) string {
	t.Helper()
	var entry struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(release.NativeEntry, &entry))
	return entry.Version
}

// A third-party Plugin that names no version anywhere is versioned by the
// exact content it is pinned to: the first 12 hex characters of its commit.
func TestUnversionedPluginIsVersionedByItsPinnedCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	server := newUnversionedGitServer(t, nil)
	work := t.TempDir()
	gitFixture(t, work, "init", "--quiet", "-b", "main")
	writeMetadata(t, work, ".claude-plugin/plugin.json", `{"name":"pinned-plugin"}`)
	writeMetadata(t, work, "skills/check/SKILL.md", "---\nname: check\ndescription: Check a change\n---\nFirst\n")
	first := server.commit(t, work, "plugin.git")
	external := t.TempDir()
	entry := func(sha string) []map[string]any {
		return []map[string]any{{"name": "pinned-plugin", "source": map[string]any{"source": "url", "url": server.url + "/plugin.git", "sha": sha}}}
	}
	writeCatalog(t, external, "external-catalog", entry(first))
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent", slog.Default())
	require.NoError(t, err)
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 30,
		CertificateAuthority: server.certificate, Sources: map[string]infrastructure.CatalogSource{"external": {URI: external}}}, store)
	require.NoError(t, err)

	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	release, err := store.Get(ctx, "pinned-plugin")
	require.NoError(t, err)
	require.Equal(t, first[:12], release.Version)
	require.Equal(t, first[:12], nativeVersion(t, release), "the published entry states the version clients load")

	server.requests.Store(0)
	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	require.Zero(t, server.requests.Load(), "an unchanged pin reuses the retained release without fetching")
	unchanged, err := store.Get(ctx, "pinned-plugin")
	require.NoError(t, err)
	require.Equal(t, release, unchanged)

	writeMetadata(t, work, "skills/check/SKILL.md", "---\nname: check\ndescription: Check a change\n---\nSecond\n")
	second := server.commit(t, work, "plugin.git")
	writeCatalog(t, external, "external-catalog", entry(second))
	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	latest, err := store.Get(ctx, "pinned-plugin")
	require.NoError(t, err)
	require.Equal(t, second[:12], latest.Version, "a new commit is a new release")
	retained, err := store.PublishedRelease(ctx, "external", "pinned-plugin", first[:12])
	require.NoError(t, err)
	require.NotNil(t, retained, "the earlier release stays retained")
}

// A package inside a third-party catalog repository is pinned to the catalog
// commit, which also versions it when nothing names a version. An archive
// pinned by checksum is versioned by that checksum.
func TestUnversionedCatalogAndArchivePackagesAreVersionedByTheirPins(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	archive := metadataZIP(t, map[string]string{"bundle/skills/check/SKILL.md": "---\nname: check\ndescription: Check\n---\nArchived\n"})
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	server := newUnversionedGitServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(archive) }))
	catalog := t.TempDir()
	gitFixture(t, catalog, "init", "--quiet", "-b", "main")
	gitFixture(t, catalog, "remote", "add", "origin", server.url+"/catalog.git")
	writeMetadata(t, catalog, "plugins/bundled-plugin/skills/check/SKILL.md", "---\nname: check\ndescription: Check\n---\nBundled\n")
	writeCatalog(t, catalog, "external-catalog", []map[string]any{
		{"name": "bundled-plugin", "source": "./plugins/bundled-plugin"},
		{"name": "archived-plugin", "source": map[string]any{"source": "archive", "url": server.url + "/archived.zip", "sha256": digest}},
	})
	commit := server.commit(t, catalog, "catalog.git")
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent", slog.Default())
	require.NoError(t, err)
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 30,
		CertificateAuthority: server.certificate, Sources: map[string]infrastructure.CatalogSource{"external": {URI: catalog}}}, store)
	require.NoError(t, err)

	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	bundled, err := store.Get(ctx, "bundled-plugin")
	require.NoError(t, err)
	require.Equal(t, commit[:12], bundled.Version)
	archived, err := store.Get(ctx, "archived-plugin")
	require.NoError(t, err)
	require.Equal(t, digest[:12], archived.Version)

	server.requests.Store(0)
	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	require.Zero(t, server.requests.Load(), "unchanged pins reuse the retained releases without fetching")
}

// An entry whose source runs a command on the installing machine can never be
// acquired by the platform: it is skipped and logged, not a failure. A source
// kind that is merely unknown still fails.
func TestLocalCommandSourceIsSkippedNotFailed(t *testing.T) {
	external := t.TempDir()
	writeCatalog(t, external, "external-catalog", []map[string]any{{"name": "local-tool", "source": map[string]any{"source": "command", "command": "tool plugin path", "mode": "link"}}})
	logs := &lockedBuffer{}
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent", slog.New(slog.NewJSONHandler(logs, nil)))
	require.NoError(t, err)
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 1, RequestTimeoutSeconds: 5,
		Sources: map[string]infrastructure.CatalogSource{"external": {URI: external}}}, store)
	require.NoError(t, err)

	require.NoError(t, synchronizer.SynchronizeCatalog(t.Context(), false))
	_, err = store.Get(t.Context(), "local-tool")
	require.ErrorIs(t, err, application.ErrReleaseNotFound)
	var record struct {
		Level, Source, Slug string
	}
	require.NoError(t, json.Unmarshal(logs.body.Bytes(), &record), logs.String())
	require.Equal(t, struct{ Level, Source, Slug string }{"WARN", "external", "local-tool"}, record)

	writeCatalog(t, external, "external-catalog", []map[string]any{{"name": "mystery-tool", "source": map[string]any{"source": "mystery"}}})
	require.Error(t, synchronizer.SynchronizeCatalog(t.Context(), false))
}

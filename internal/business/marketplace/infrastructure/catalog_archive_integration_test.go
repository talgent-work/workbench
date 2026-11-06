//go:build integration

package infrastructure_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
)

func metadataZIP(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	for name, content := range files {
		file, e := writer.Create(name)
		require.NoError(t, e)
		_, e = file.Write([]byte(content))
		require.NoError(t, e)
	}
	require.NoError(t, writer.Close())
	return body.Bytes()
}

func TestArchiveSynchronizationParallelIncrementalAndFailureRetention(t *testing.T) {
	// Reach real HTTPS, ZIP extraction, metadata parsing and MySQL publication.
	// Two gated requests prove parallelism; two entries sharing a URL prove reuse.
	body := metadataZIP(t, map[string]string{"bundle/skills/check/SKILL.md": "---\nname: check\ndescription: Check changes\n---\nReview changes"})
	digest := sha256.Sum256(body)
	var downloads, active, peak atomic.Int32
	var fail atomic.Bool
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		downloads.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		if fail.Load() {
			http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		select {
		case entered <- struct{}{}:
		default:
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(body)
	})
	server := newCatalogHTTPSServer(t, mux)
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	source := t.TempDir()
	entry := func(name, version, file string) map[string]any {
		return map[string]any{"name": name, "version": version, "source": map[string]any{"source": "archive", "url": server.URL + file, "sha256": hex.EncodeToString(digest[:])}}
	}
	entries := []map[string]any{entry("alpha", "1.0.0", "/shared.zip"), entry("charlie", "1.0.0", "/second.zip"), entry("beta", "1.0.0", "/shared.zip")}
	writeCatalog(t, source, "upstream", entries)
	store, e := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, e)
	config := infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 5, CertificateAuthority: certificate, Sources: []infrastructure.CatalogSource{{Name: "upstream", URI: source}}}
	syncer, e := infrastructure.NewCatalogSynchronizer(config, store)
	require.NoError(t, e)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- syncer.SynchronizeCatalog(ctx, false) }()
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("downloads did not run concurrently")
		}
	}
	close(release)
	require.NoError(t, <-done)
	require.EqualValues(t, 2, peak.Load())
	require.EqualValues(t, 2, downloads.Load(), "same archive is acquired once across workers")
	for _, slug := range []string{"alpha", "beta", "charlie"} {
		r, e := store.Get(ctx, slug)
		require.NoError(t, e)
		require.Equal(t, "1.0.0", r.Version)
		require.Equal(t, slug+":check", r.Skills[0].Name)
	}
	// An unchanged version remains usable even when its download endpoint is down.
	fail.Store(true)
	require.NoError(t, syncer.SynchronizeCatalog(ctx, false))
	require.EqualValues(t, 2, downloads.Load(), "unchanged versions must not download or unpack")
	require.Error(t, syncer.SynchronizeCatalog(ctx, true))
	require.EqualValues(t, 4, downloads.Load(), "force reacquires each distinct archive despite unchanged versions")
	retained, e := store.Get(ctx, "alpha")
	require.NoError(t, e)
	require.Equal(t, "1.0.0", retained.Version)
	entries[1] = entry("charlie", "2.0.0", "/second.zip")
	writeCatalog(t, source, "upstream", entries)
	require.Error(t, syncer.SynchronizeCatalog(ctx, false))
	r, e := store.Get(ctx, "charlie")
	require.NoError(t, e)
	require.Equal(t, "1.0.0", r.Version)
	fail.Store(false)
	entries = append(entries, entry("delta", "1.0.0", "/new.zip"))
	entries[1]["source"].(map[string]any)["sha256"] = string(bytes.Repeat([]byte{'0'}, 64))
	entries = append(entries, entry("first-failure", "1.0.0", "/invalid.zip"))
	entries[4]["source"].(map[string]any)["sha256"] = string(bytes.Repeat([]byte{'0'}, 64))
	writeCatalog(t, source, "upstream", entries)
	require.Error(t, syncer.SynchronizeCatalog(ctx, false))
	r, e = store.Get(ctx, "charlie")
	require.NoError(t, e)
	require.Equal(t, "1.0.0", r.Version)
	_, e = store.Get(ctx, "delta")
	require.NoError(t, e)
	_, e = store.Get(ctx, "first-failure")
	require.ErrorIs(t, e, application.ErrReleaseNotFound)
}

func TestAcquisitionDeadlinePublishesCompletedPlugins(t *testing.T) {
	body := metadataZIP(t, map[string]string{"skills/check/SKILL.md": "Check changes"})
	digest := sha256.Sum256(body)
	blocked := make(chan struct{})
	server := newCatalogHTTPSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.zip" {
			close(blocked)
			<-r.Context().Done()
			return
		}
		_, _ = w.Write(body)
	}))
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600))
	source := t.TempDir()
	entries := []map[string]any{}
	for _, name := range []string{"fast", "slow"} {
		entries = append(entries, map[string]any{"name": name, "version": "1.0.0", "source": map[string]any{"source": "archive", "url": server.URL + "/" + name + ".zip", "sha256": hex.EncodeToString(digest[:])}})
	}
	writeCatalog(t, source, "upstream", entries)
	store, e := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, e)
	syncer, e := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 10, CertificateAuthority: certificate, Sources: []infrastructure.CatalogSource{{Name: "upstream", URI: source}}}, store)
	require.NoError(t, e)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	e = syncer.SynchronizeCatalog(ctx, false)
	require.ErrorIs(t, e, context.DeadlineExceeded)
	select {
	case <-blocked:
	default:
		t.Fatal("must exercise an active download")
	}
	_, e = store.Get(t.Context(), "fast")
	require.NoError(t, e, "completed Plugin is published despite another acquisition timing out")
	_, e = store.Get(t.Context(), "slow")
	require.ErrorIs(t, e, application.ErrReleaseNotFound)
}

func TestUnchangedManifestVersionReusesRelease(t *testing.T) {
	// A stale index must not hide a newer package manifest version.
	source := t.TempDir()
	writeMetadata(t, source, "plugin/.claude-plugin/plugin.json", `{"name":"owned","version":"1.0.0"}`)
	writeMetadata(t, source, "plugin/skills/check/SKILL.md", "Original")
	writeCatalog(t, source, "owned", []map[string]any{{"name": "catalog-alias", "version": "1.0.0", "description": "Alias title", "source": "./plugin"}, {"name": "owned", "description": "Canonical title", "source": "./plugin"}})
	store, e := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, e)
	syncer, e := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 5, ArchiveBaseURL: "https://example.invalid/archives", Sources: []infrastructure.CatalogSource{{Name: "owned", URI: source, Owned: true}}}, store)
	require.NoError(t, e)
	require.NoError(t, syncer.SynchronizeCatalog(t.Context(), false))
	before, e := store.Get(t.Context(), "owned")
	require.NoError(t, e)
	require.Equal(t, "Canonical title", before.Description)
	require.Empty(t, before.CatalogEntryName)
	require.NoError(t, os.RemoveAll(filepath.Join(source, "plugin/skills")))
	require.NoError(t, syncer.SynchronizeCatalog(t.Context(), false))
	after, e := store.Get(t.Context(), "owned")
	require.NoError(t, e)
	require.Equal(t, before, after, "same version must reuse retained metadata without reparsing content")
	require.ErrorIs(t, syncer.SynchronizeCatalog(t.Context(), true), domain.ErrReleaseChanged)
	after, e = store.Get(t.Context(), "owned")
	require.NoError(t, e)
	require.Equal(t, before, after, "force cannot replace conflicting content under the same version")
	// Contents are not stored, yet a body-only change under the same version is
	// still a changed package: its archive differs.
	writeMetadata(t, source, "plugin/skills/check/SKILL.md", "Edited body, same version")
	require.ErrorIs(t, syncer.SynchronizeCatalog(t.Context(), true), domain.ErrReleaseChanged)
	writeMetadata(t, source, "plugin/.claude-plugin/plugin.json", `{"name":"owned","version":"2.0.0"}`)
	writeMetadata(t, source, "plugin/skills/check/SKILL.md", "Updated")
	require.NoError(t, syncer.SynchronizeCatalog(t.Context(), false))
	after, e = store.Get(t.Context(), "owned")
	require.NoError(t, e)
	require.Equal(t, "2.0.0", after.Version)
	require.Equal(t, "owned:check", after.Skills[0].Name, "the Skill is declared; its body stays in the package archive")
}

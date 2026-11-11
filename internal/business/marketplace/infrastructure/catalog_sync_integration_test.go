//go:build integration

package infrastructure_test

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/cgi"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
)

func gitFixture(t *testing.T, directory string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, args...)...)
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	return strings.TrimSpace(string(output))
}

func writeCatalog(t *testing.T, root, name string, entries []map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"name": name, "owner": map[string]string{"name": "Fixture"}, "plugins": entries})
	require.NoError(t, err)
	writeMetadata(t, root, ".claude-plugin/marketplace.json", string(data))
}

func TestCatalogSynchronization(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	gitRoot := t.TempDir()
	work := filepath.Join(gitRoot, "work")
	require.NoError(t, os.Mkdir(work, 0o700))
	gitFixture(t, work, "init", "-b", "main")
	writeMetadata(t, work, ".claude-plugin/plugin.json", `{"name":"Actual-Plugin","version":"1.0.0","commands":{"hello":{"content":"Say hello to $ARGUMENTS","description":"Inline greeting","argumentHint":"[name]"},"status":{"source":"./custom/status.md","description":"Declared status","argumentHint":"[env]"}},"userConfig":{"token":{"type":"string","title":"Token","description":"Token","sensitive":true,"required":true}}}`)
	writeMetadata(t, work, "skills/check/SKILL.md", "---\nname: check\ndescription: Check a change\n---\nThird-party capability\n")
	writeMetadata(t, work, "custom/status.md", "---\nname: ignored-file-name\ndescription: File description\nargument-hint: old-hint\n---\nShow status for $ARGUMENTS\n")
	writeMetadata(t, work, "commands/hidden.md", "This default command must be replaced by explicit commands\n")
	gitFixture(t, work, "add", ".")
	gitFixture(t, work, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "first version")
	firstSHA := gitFixture(t, work, "rev-parse", "HEAD")
	bare := filepath.Join(gitRoot, "plugin.git")
	gitFixture(t, gitRoot, "clone", "--bare", work, bare)
	gitExecPath := gitFixture(t, gitRoot, "--exec-path")
	mux := http.NewServeMux()
	mux.Handle("/", &cgi.Handler{Path: filepath.Join(gitExecPath, "git-http-backend"), Dir: gitRoot, Root: "/", Env: []string{"GIT_PROJECT_ROOT=" + gitRoot, "GIT_HTTP_EXPORT_ALL=1"}})
	server := newCatalogHTTPSServer(t, mux)
	t.Cleanup(server.Close)
	certificate := filepath.Join(gitRoot, "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	owned, external := t.TempDir(), t.TempDir()
	writeMetadata(t, owned, "plugins/owned-plugin/.claude-plugin/plugin.json", `{"name":"owned-plugin","version":"1.0.0"}`)
	writeMetadata(t, owned, "plugins/owned-plugin/agents/role.md", "---\nname: role\ndescription: Role\n---\nOwned capability\n")
	writeCatalog(t, owned, "owned-catalog", []map[string]any{{"name": "owned-plugin", "source": "./plugins/owned-plugin"}})
	writeCatalog(t, external, "external-catalog", []map[string]any{{"name": "catalog-alias", "source": map[string]any{"source": "url", "url": server.URL + "/plugin.git"}}})
	content := t.TempDir()
	db := catalogDatabase(t)
	store, err := infrastructure.NewCatalogStore(db, content, "talgent", "Talgent")
	require.NoError(t, err)
	mux.Handle(marketplacehttp.ContentPath, marketplacehttp.NewCatalogHandler(application.NewApplication(nil, nil, store, nil, nil, application.ConnectorRepositories{}), slog.Default()))
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 45,
		CertificateAuthority: certificate, ArchiveBaseURL: server.URL + "/marketplace/archives",
		Sources: map[string]infrastructure.CatalogSource{"owned": {URI: owned, Owned: true}, "external": {URI: external}}}, store)
	require.NoError(t, err)
	synchronize := func() error { return synchronizer.SynchronizeCatalog(ctx, false) }

	require.NoError(t, synchronize())
	release, err := store.Get(ctx, "Actual-Plugin")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", release.Version)
	require.Equal(t, "Actual-Plugin:check", release.Skills[0].Name)
	require.Len(t, release.Skills, 3, "explicit command maps replace the default commands directory")
	require.Equal(t, application.SkillComponent{Name: "Actual-Plugin:hello", Description: "Inline greeting", ArgumentHint: "[name]"}, release.Skills[1])
	require.Equal(t, "Actual-Plugin:status", release.Skills[2].Name, "object key overrides the source file/frontmatter name")
	require.Equal(t, "Declared status", release.Skills[2].Description)
	require.Equal(t, "[env]", release.Skills[2].ArgumentHint)
	require.True(t, release.Parameters["token"].Required, "package acquisition does not invent personal parameters")
	var entry struct {
		Source struct {
			SHA string `json:"sha"`
		}
	}
	require.NoError(t, json.Unmarshal(release.NativeEntry, &entry))
	require.Equal(t, firstSHA, entry.Source.SHA)
	ownedRelease, err := store.Get(ctx, "owned-plugin")
	require.NoError(t, err)
	var ownedEntry struct {
		Source struct {
			Source string
			URL    string
			SHA256 string
		}
	}
	require.NoError(t, json.Unmarshal(ownedRelease.NativeEntry, &ownedEntry))
	require.Equal(t, "archive", ownedEntry.Source.Source)
	archiveFile := filepath.Join(content, "archives", ownedEntry.Source.SHA256+".zip")
	data, err := os.ReadFile(archiveFile)
	require.NoError(t, err)
	hash := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(hash[:]), ownedEntry.Source.SHA256)
	archive, err := zip.OpenReader(archiveFile)
	require.NoError(t, err)
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	require.NoError(t, archive.Close())
	require.Contains(t, names, "agents/role.md")
	require.NoError(t, synchronize(), "same-source refresh keeps immutable content stable")
	writeMetadata(t, work, ".claude-plugin/plugin.json", `{"name":"Actual-Plugin","version":"2.0.0"}`)
	gitFixture(t, work, "add", ".")
	gitFixture(t, work, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "second version")
	gitFixture(t, bare, "fetch", work, "main:main")
	require.NoError(t, synchronize())
	latest, err := store.Get(ctx, "Actual-Plugin")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", latest.Version)
	require.Equal(t, "1.0.0", release.Version, "previously read release remains unchanged")
	writeMetadata(t, owned, "plugins/owned-plugin/.claude-plugin/plugin.json", `{"name":"owned-plugin","version":"2.0.0"}`)
	writeCatalog(t, external, "external-catalog", []map[string]any{{"name": "catalog-alias", "version": "wrong", "source": map[string]any{"source": "url", "url": server.URL + "/plugin.git"}}})
	require.NoError(t, synchronize(), "package version overrides a stale index version")
	writeMetadata(t, work, ".claude-plugin/plugin.json", `{"name":"invalid/name","version":"3.0.0"}`)
	gitFixture(t, work, "add", ".")
	gitFixture(t, work, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "invalid package identity")
	gitFixture(t, bare, "fetch", work, "main:main")
	writeMetadata(t, owned, "plugins/owned-plugin/.claude-plugin/plugin.json", `{"name":"owned-plugin","version":"3.0.0"}`)
	require.Error(t, synchronize())
	latest, err = store.Get(ctx, "Actual-Plugin")
	require.NoError(t, err)
	require.Equal(t, "2.0.0", latest.Version, "failed Plugin retains its last valid release")
	updated, err := store.Get(ctx, "owned-plugin")
	require.NoError(t, err)
	require.Equal(t, "3.0.0", updated.Version, "another Plugin still updates during the failed refresh")
}

// Packages are read without credentials, so a catalog entry naming an SSH
// remote fails without reaching an SSH client.
func TestCatalogPackageOverSSHIsRefused(t *testing.T) {
	client := t.TempDir()
	called := filepath.Join(client, "called")
	require.NoError(t, os.WriteFile(filepath.Join(client, "ssh"), []byte("#!/bin/sh\ntouch '"+called+"'\nexit 255\n"), 0o755))
	t.Setenv("PATH", client+string(os.PathListSeparator)+os.Getenv("PATH"))
	external := t.TempDir()
	writeCatalog(t, external, "external-catalog", []map[string]any{{"name": "ssh-plugin", "source": map[string]any{"source": "url", "url": "git@git.example.invalid:example/plugin.git"}}})
	store, err := infrastructure.NewCatalogStore(catalogDatabase(t), t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 1, RequestTimeoutSeconds: 5,
		Sources: map[string]infrastructure.CatalogSource{"external": {URI: external}}}, store)
	require.NoError(t, err)
	require.Error(t, synchronizer.SynchronizeCatalog(t.Context(), false))
	require.NoFileExists(t, called, "no SSH client is run")
	_, err = store.Get(t.Context(), "ssh-plugin")
	require.ErrorIs(t, err, application.ErrReleaseNotFound)
}

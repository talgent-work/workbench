//go:build integration

package infrastructure_test

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
)

// fakeSSH stands in for the SSH client on PATH: it serves git over the local
// filesystem only to the expected deploy key, records how it was called, and
// otherwise answers like a refusing host whose message carries a credential.
func fakeSSH(t *testing.T, expectedKey string) (directory, calls string) {
	t.Helper()
	directory = t.TempDir()
	calls = filepath.Join(directory, "calls")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + calls + `'
key=""; prev=""
for arg; do
  [ "$prev" = "-i" ] && key="$arg"
  [ "$arg" = "-G" ] && exit 0
  prev="$arg"
done
if ! cmp -s "$key" '` + expectedKey + `'; then
  echo "git@git.example.invalid: Permission denied (publickey) for https://deploy:s3cret@git.example.invalid/catalog.git?token=s3cret" >&2
  echo "fatal: Could not read from remote repository." >&2
  exit 255
fi
exec sh -c "$prev"
`
	require.NoError(t, os.WriteFile(filepath.Join(directory, "ssh"), []byte(script), 0o755))
	return directory, calls
}

func TestPrivateCatalogSourceSynchronizesWithItsDeployKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	work := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(work, 0o700))
	gitFixture(t, work, "init", "-b", "main")
	writeMetadata(t, work, "plugins/private-plugin/.claude-plugin/plugin.json", `{"name":"private-plugin","version":"1.0.0"}`)
	writeMetadata(t, work, "plugins/private-plugin/agents/role.md", "---\nname: role\ndescription: Role\n---\nPrivate capability\n")
	writeCatalog(t, work, "private-catalog", []map[string]any{{"name": "private-plugin", "source": "./plugins/private-plugin"}})
	commit := func(message string) {
		gitFixture(t, work, "add", ".")
		gitFixture(t, work, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", message)
	}
	commit("first version")
	bare := filepath.Join(root, "catalog.git")
	gitFixture(t, root, "clone", "--bare", work, bare)

	secrets := t.TempDir()
	key, known := filepath.Join(secrets, "ssh-privatekey"), filepath.Join(secrets, "known_hosts")
	require.NoError(t, os.WriteFile(key, []byte("fixture deploy key\n"), 0o644), "a Secret volume's mode is not one SSH accepts")
	require.NoError(t, os.WriteFile(known, []byte("git.example.invalid ssh-ed25519 AAAAfixture\n"), 0o644))
	expected := filepath.Join(t.TempDir(), "expected-key")
	require.NoError(t, os.WriteFile(expected, []byte("fixture deploy key\n"), 0o600))
	ssh, calls := fakeSSH(t, expected)
	t.Setenv("PATH", ssh+string(os.PathListSeparator)+os.Getenv("PATH"))

	db := catalogDatabase(t)
	content := t.TempDir()
	store, err := infrastructure.NewCatalogStore(db, content, "talgent", "Talgent")
	require.NoError(t, err)
	server := newCatalogHTTPSServer(t, marketplacehttp.NewCatalogHandler(application.NewApplication(nil, nil, store, nil, nil, application.ConnectorRepositories{}), slog.Default()))
	defer server.Close()
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 1, RequestTimeoutSeconds: 30, CertificateAuthority: certificate, ArchiveBaseURL: server.URL + "/marketplace/archives",
		Sources: map[string]infrastructure.CatalogSource{"private": {URI: "ssh://git@git.example.invalid" + bare, Owned: true, Credentials: &infrastructure.CatalogSourceCredentials{SSHKeyFile: key, KnownHostsFile: known}}}}, store)
	require.NoError(t, err)

	require.NoError(t, synchronizer.SynchronizeCatalog(ctx, false))
	release, err := store.Get(ctx, "private-plugin")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", release.Version)
	var entry struct {
		Source struct{ Source, URL string }
	}
	require.NoError(t, json.Unmarshal(release.NativeEntry, &entry))
	require.Equal(t, "archive", entry.Source.Source, "a Work installs the private package from the platform's archive")
	require.True(t, strings.HasPrefix(entry.Source.URL, server.URL+"/marketplace/archives/"))
	recorded, err := os.ReadFile(calls)
	require.NoError(t, err)
	for _, want := range []string{"-i ", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "UserKnownHostsFile=", "git@git.example.invalid"} {
		require.Contains(t, string(recorded), want)
	}

	// A wrong deploy key fails the source visibly; the published release
	// stays, and the diagnosis says what the host answered, without the URL's
	// credential or query.
	require.NoError(t, os.WriteFile(key, []byte("a revoked deploy key\n"), 0o644))
	writeMetadata(t, work, "plugins/private-plugin/.claude-plugin/plugin.json", `{"name":"private-plugin","version":"2.0.0"}`)
	commit("second version")
	gitFixture(t, bare, "fetch", work, "main:main")
	runs := infrastructure.NewSynchronizationRuns(db)
	var logs bytes.Buffer
	app := application.NewCatalogSynchronization(runs, synchronizer, nil, time.Minute, true, slog.New(slog.NewTextHandler(&logs, nil)))
	now := time.Now()
	run, err := runs.Reserve(ctx, application.SynchronizationRun{ID: uuid.Must(uuid.NewV7()).String(), Status: "queued", RequestedAt: now.UnixMilli(), Deadline: now.Add(time.Minute).UnixMilli()})
	require.NoError(t, err)
	require.Error(t, app.Execute(ctx, run.ID))
	latest, err := app.Latest(ctx)
	require.NoError(t, err)
	require.Equal(t, "partial_failure", latest.Status, "other sources still publish")
	require.Len(t, latest.Failures, 1)
	failure := latest.Failures[0]
	require.Equal(t, "private", failure.Source)
	require.Empty(t, failure.Slug, "the source as a whole failed")
	require.Contains(t, failure.Reason, "Permission denied (publickey)")
	require.Contains(t, failure.Reason, "https://git.example.invalid/catalog.git")
	for _, diagnosis := range []string{failure.Reason, logs.String()} {
		require.NotContains(t, diagnosis, "s3cret")
		require.NotContains(t, diagnosis, "revoked deploy key")
	}
	require.Contains(t, logs.String(), "Permission denied (publickey)")
	retained, err := store.Get(ctx, "private-plugin")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", retained.Version)
}

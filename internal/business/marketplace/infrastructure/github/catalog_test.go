package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestCatalog_ObserveAccessUsesEveryUserInstallation(t *testing.T) {
	var sawRepositoryRequest bool
	cipher, err := NewTokenCipher(enabledConfig(t))
	if err != nil {
		t.Fatalf("NewTokenCipher: %v", err)
	}
	ciphertext, err := cipher.Encrypt("gho_user_catalog_token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "github.com" {
			t.Fatalf("test must not egress to github.com")
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Fatalf("Accept=%q", got)
		}
		if got := r.Header.Get("X-GitHub-Api-Version"); got != "2026-03-10" {
			t.Fatalf("api version=%q", got)
		}

		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			t.Fatal("catalog must not request an installation token for user-visible repository listing")
		case "/user/installations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"installations":[{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write"}}]}`))
		case "/user/installations/42/repositories":
			sawRepositoryRequest = true
			if r.Method != http.MethodGet {
				t.Fatalf("method=%s", r.Method)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer gho_user_catalog_token" {
				t.Fatalf("Authorization=%q", got)
			}
			if got := r.URL.Query().Get("per_page"); got != "100" {
				t.Fatalf("per_page=%q", got)
			}
			if got := r.URL.Query().Get("page"); got != "1" {
				t.Fatalf("page=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"repositories":[{"id":987,"full_name":"acme/api","clone_url":"https://github.com/acme/api.git","html_url":"https://github.com/acme/api","default_branch":"main"}]}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.APIBaseURL = server.URL
	cfg.APIVersion = "2026-03-10"
	catalog, err := NewCatalog(cfg, &fakeAuthorizationRepository{
		authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour)),
	}, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	scope, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	repos := scope.Repositories()
	if !sawRepositoryRequest {
		t.Fatal("missing expected user installation repositories API call")
	}
	if len(repos) != 1 {
		t.Fatalf("repos=%+v", repos)
	}
	repo := repos[0]
	if repo.RepoID != "github:987" || repo.RepoName != "acme/api" || repo.CloneURL != "https://github.com/acme/api.git" || repo.WebURL != "https://github.com/acme/api" || repo.DefaultBranch != "main" {
		t.Fatalf("repo mapping=%+v", repo)
	}
}

// Intent (real): repository catalog requests should refresh an expired GitHub
// user token before calling GitHub so Profile and StartWork bindable repos do
// not fail with provider unavailable after token expiry.
func TestCatalog_ObserveAccessRefreshesExpiredUserAuthorization(t *testing.T) {
	cipher := mustTokenCipher(t)
	accessCiphertext, err := cipher.Encrypt("expired-access-token")
	if err != nil {
		t.Fatalf("Encrypt access: %v", err)
	}
	refreshCiphertext, err := cipher.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("Encrypt refresh: %v", err)
	}
	repo := &fakeAuthorizationRepository{
		authorization: mustActiveAuthorization(t, "user-1", accessCiphertext, time.Now().UTC().Add(-time.Minute)),
	}
	repo.authorization.RefreshTokenCiphertext = refreshCiphertext

	var sawRefresh bool
	var sawRepositoryRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if got := r.Form.Get("grant_type"); got != "refresh_token" {
				t.Fatalf("grant_type=%q", got)
			}
			if got := r.Form.Get("refresh_token"); got != "refresh-token" {
				t.Fatalf("refresh_token form value was not the expected test value")
			}
			sawRefresh = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh-access-token","refresh_token":"fresh-refresh-token","expires_in":3600,"refresh_token_expires_in":28800}`))
		case "/user/installations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"installations":[{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write"}}]}`))
		case "/user/installations/42/repositories":
			sawRepositoryRequest = true
			if got := r.Header.Get("Authorization"); got != "Bearer fresh-access-token" {
				t.Fatalf("Authorization=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"repositories":[{"id":987,"full_name":"acme/api","clone_url":"https://github.com/acme/api.git","html_url":"https://github.com/acme/api","default_branch":"main"}]}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg, repo, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	scope, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	repositories := scope.Repositories()
	if !sawRefresh {
		t.Fatal("missing refresh token request")
	}
	if !sawRepositoryRequest {
		t.Fatal("missing repository request")
	}
	if repo.saveCalls != 1 {
		t.Fatalf("saveCalls=%d, want 1", repo.saveCalls)
	}
	if len(repositories) != 1 || repositories[0].RepoID != "github:987" {
		t.Fatalf("repositories=%+v", repositories)
	}
}

// Intent (real): a GitHub 401 from a user-token API call should force one
// refresh and retry once so catalog reads recover from provider-side token revocation.
func TestCatalog_ObserveAccessRefreshesOnceAfterGitHubUnauthorized(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cipher := mustTokenCipher(t)
	accessCiphertext, err := cipher.Encrypt("stale-access-token")
	if err != nil {
		t.Fatalf("Encrypt access: %v", err)
	}
	refreshCiphertext, err := cipher.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("Encrypt refresh: %v", err)
	}
	auth := mustActiveAuthorization(t, "user-1", accessCiphertext, now.Add(time.Hour))
	auth.RefreshTokenCiphertext = refreshCiphertext
	auth.RefreshTokenExpiresAt = now.Add(24 * time.Hour)
	repo := &fakeAuthorizationRepository{authorization: auth}

	var repositoryCalls int
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			sawRefresh = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh-access-token","refresh_token":"fresh-refresh-token","expires_in":3600,"refresh_token_expires_in":28800}`))
		case "/user/installations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"installations":[{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write"}}]}`))
		case "/user/installations/42/repositories":
			repositoryCalls++
			if repositoryCalls == 1 {
				if got := r.Header.Get("Authorization"); got != "Bearer stale-access-token" {
					t.Fatalf("first Authorization=%q", got)
				}
				http.Error(w, "bad credentials", http.StatusUnauthorized)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer fresh-access-token" {
				t.Fatalf("retry Authorization=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"repositories":[{"id":987,"full_name":"acme/api","clone_url":"https://github.com/acme/api.git","html_url":"https://github.com/acme/api","default_branch":"main"}]}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg, repo, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	catalog.now = func() time.Time { return now }

	scope, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	repositories := scope.Repositories()
	if !sawRefresh || repositoryCalls != 2 {
		t.Fatalf("refresh/repository calls=%v/%d, want true/2", sawRefresh, repositoryCalls)
	}
	if len(repositories) != 1 || repositories[0].RepoID != "github:987" {
		t.Fatalf("repositories=%+v", repositories)
	}
}

// Intent (real): if GitHub still rejects the user token after the one forced
// refresh, catalog reads must fail closed instead of looping or returning stale data.
func TestCatalog_ObserveAccessFailsClosedWhenUnauthorizedAfterRefresh(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cipher := mustTokenCipher(t)
	accessCiphertext, err := cipher.Encrypt("stale-access-token")
	if err != nil {
		t.Fatalf("Encrypt access: %v", err)
	}
	refreshCiphertext, err := cipher.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("Encrypt refresh: %v", err)
	}
	auth := mustActiveAuthorization(t, "user-1", accessCiphertext, now.Add(time.Hour))
	auth.RefreshTokenCiphertext = refreshCiphertext
	auth.RefreshTokenExpiresAt = now.Add(24 * time.Hour)
	repo := &fakeAuthorizationRepository{authorization: auth}

	var repositoryCalls int
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			sawRefresh = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"fresh-access-token","refresh_token":"fresh-refresh-token","expires_in":3600,"refresh_token_expires_in":28800}`))
		case "/user/installations":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"installations":[{"id":42,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write"}}]}`))
		case "/user/installations/42/repositories":
			repositoryCalls++
			http.Error(w, "bad credentials", http.StatusUnauthorized)
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg, repo, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	catalog.now = func() time.Time { return now }

	_, err = catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	var providerErr *application.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != providerErrorGitHubTokenRefreshFailed {
		t.Fatalf("err=%v, want token refresh failed provider error", err)
	}
	if !sawRefresh || repositoryCalls != 2 {
		t.Fatalf("refresh/repository calls=%v/%d, want true/2", sawRefresh, repositoryCalls)
	}
}

func TestCatalog_ObserveAccessMapsMissingAuthorizationToProviderUnavailable(t *testing.T) {
	catalog, err := NewCatalog(enabledConfig(t), &fakeAuthorizationRepository{err: domain.ErrProviderNotFound}, &passthroughTokenCipher{})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	_, err = catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

func TestCatalog_ObserveAccessMapsDecryptFailureToProviderUnavailable(t *testing.T) {
	catalog, err := NewCatalog(enabledConfig(t), &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", "not-a-supported-ciphertext", time.Now().UTC().Add(time.Hour)),
	}, mustTokenCipher(t))
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	_, err = catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return string(pem.EncodeToMemory(block))
}

// One authorization reaches every installation: the personal account and each
// organization. Repositories are their union; a vanished installation only
// narrows the scope.
func TestCatalog_ObserveAccessCoversEveryInstallation(t *testing.T) {
	cipher := mustTokenCipher(t)
	ciphertext, err := cipher.Encrypt("gho_user_catalog_token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/installations":
			_, _ = w.Write([]byte(`{"installations":[
				{"id":1,"account":{"login":"octocat","type":"User","avatar_url":"https://avatars.example/octocat"},"repository_selection":"all","permissions":{"contents":"read","metadata":"read"}},
				{"id":2,"account":{"login":"acme","type":"Organization"},"repository_selection":"selected","permissions":{"contents":"write","pull_requests":"write"}},
				{"id":3,"account":{"login":"gone","type":"Organization"},"repository_selection":"all"}]}`))
		case "/user/installations/1/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"id":1,"full_name":"octocat/dotfiles","clone_url":"https://github.com/octocat/dotfiles.git","default_branch":"main"},{"id":3,"full_name":"acme/shared","clone_url":"https://github.com/acme/shared.git","default_branch":"main"}]}`))
		case "/user/installations/2/repositories":
			_, _ = w.Write([]byte(`{"repositories":[{"id":2,"full_name":"acme/api","clone_url":"https://github.com/acme/api.git","default_branch":"main"},{"id":3,"full_name":"acme/shared","clone_url":"https://github.com/acme/shared.git","default_branch":"main"}]}`))
		case "/user/installations/3/repositories":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	cfg := enabledConfig(t)
	cfg.APIBaseURL, cfg.WebBaseURL = server.URL, "https://github.example.test"
	catalog, err := NewCatalog(cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	access, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	if access.ObservedAt.IsZero() || access.Accounts[0].InstallationID != "2" || access.Accounts[1].InstallationID != "1" {
		t.Fatalf("observation=%+v", access)
	}
	if len(access.Accounts) != 2 || access.Accounts[0].Login != "acme" || access.Accounts[1].Login != "octocat" {
		t.Fatalf("accounts=%+v", access.Accounts)
	}
	if access.Accounts[0].SettingsURL != "https://github.example.test/organizations/acme/settings/installations/2" ||
		access.Accounts[1].SettingsURL != "https://github.example.test/settings/installations/1" || access.Accounts[0].RepositorySelection != "selected" {
		t.Fatalf("account details=%+v", access.Accounts)
	}
	want := []domain.ScopePermission{{Name: "contents", Access: "write"}, {Name: "metadata", Access: "read"}, {Name: "pull_requests", Access: "write"}}
	if len(access.Permissions) != len(want) {
		t.Fatalf("permissions=%+v", access.Permissions)
	}
	for i := range want {
		if access.Permissions[i] != want[i] {
			t.Fatalf("permissions=%+v, want %+v", access.Permissions, want)
		}
	}
	scope, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	repositories := scope.Repositories()
	if len(repositories) != 3 {
		t.Fatalf("repositories=%+v, want the deduplicated union", repositories)
	}
}

// Intent (real): the App declaration is read once; later Provider page reads
// do not ask GitHub again.
func TestCatalog_ManageURLAndRequestedPermissionsUseTheAppSlug(t *testing.T) {
	var appReads int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appReads++
		switch r.URL.Path {
		case "/app":
			if r.Header.Get("Authorization") == "" {
				t.Fatal("deriving the slug needs the App JWT")
			}
			_, _ = w.Write([]byte(`{"slug":"derived-app"}`))
		case "/apps/derived-app":
			if r.Header.Get("Authorization") != "" {
				t.Fatal("the public App declaration needs no credential")
			}
			_, _ = w.Write([]byte(`{"permissions":{"contents":"write"}}`))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	cfg := enabledConfig(t)
	cfg.AppSlug, cfg.APIBaseURL, cfg.WebBaseURL = "", server.URL, "https://github.example.test"
	catalog, err := NewCatalog(cfg, &fakeAuthorizationRepository{}, mustTokenCipher(t))
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	manage, err := catalog.ManageURL(context.Background(), domain.ProviderGitHub)
	if err != nil || manage != "https://github.example.test/apps/derived-app/installations/new" {
		t.Fatalf("manage=%q err=%v", manage, err)
	}
	permissions, err := catalog.RequestedPermissions(context.Background(), domain.ProviderGitHub)
	if err != nil || len(permissions) != 1 || permissions[0] != (application.ProviderPermission{Name: "contents", Access: "write"}) {
		t.Fatalf("permissions=%+v err=%v", permissions, err)
	}
	if appReads != 2 {
		t.Fatalf("App declaration reads=%d, want one slug and one declaration read", appReads)
	}
}

// Intent (real): installations are fetched concurrently, and the observation
// equals what fetching them one by one would give.
func TestCatalog_ObserveAccessFetchesManyInstallationsConcurrently(t *testing.T) {
	cipher := mustTokenCipher(t)
	ciphertext, err := cipher.Encrypt("gho_user_catalog_token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	const installations = 12
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user/installations" {
			var list []string
			for i := 1; i <= installations; i++ {
				list = append(list, fmt.Sprintf(`{"id":%d,"account":{"login":"org-%02d","type":"Organization"},"repository_selection":"selected"}`, i, i))
			}
			_, _ = w.Write([]byte(`{"installations":[` + strings.Join(list, ",") + `]}`))
			return
		}
		var id int
		if _, err := fmt.Sscanf(r.URL.Path, "/user/installations/%d/repositories", &id); err != nil {
			t.Errorf("unexpected request path %s", r.URL.Path)
			return
		}
		time.Sleep(10 * time.Millisecond)
		_, _ = fmt.Fprintf(w, `{"repositories":[{"id":%d,"full_name":"org-%02d/repo"}]}`, id, id)
	}))
	defer server.Close()
	cfg := enabledConfig(t)
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	scope, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if err != nil {
		t.Fatalf("ObserveAccess: %v", err)
	}
	if len(scope.Accounts) != installations {
		t.Fatalf("accounts=%d", len(scope.Accounts))
	}
	for i, account := range scope.Accounts {
		want := fmt.Sprintf("org-%02d", i+1)
		if account.Login != want || account.InstallationID != strconv.Itoa(i+1) || len(account.Repositories) != 1 || account.Repositories[0].RepoName != want+"/repo" {
			t.Fatalf("account %d=%+v", i, account)
		}
	}
}

// Intent (real): a refused installation listing, such as a secondary rate
// limit, fails the observation; only a missing installation narrows it.
func TestCatalog_ObserveAccessFailsOnRefusedInstallation(t *testing.T) {
	cipher := mustTokenCipher(t)
	ciphertext, err := cipher.Encrypt("gho_user_catalog_token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/user/installations":
			_, _ = w.Write([]byte(`{"installations":[{"id":1,"account":{"login":"acme","type":"Organization"}}]}`))
		default:
			http.Error(w, `{"message":"You have exceeded a secondary rate limit"}`, http.StatusForbidden)
		}
	}))
	defer server.Close()
	cfg := enabledConfig(t)
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if _, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub); !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

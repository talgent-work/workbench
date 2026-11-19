package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestCatalog_ObserveAccessUsesEveryUserInstallation(t *testing.T) {
	var sawRepositoryRequest bool
	cipher := mustTokenCipher(t)
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
	catalog, err := testCatalog(t, cfg, &fakeAuthorizationRepository{
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
	catalog, err := testCatalog(t, cfg, repo, cipher)
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
	catalog, err := testCatalog(t, cfg, repo, cipher)
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
	catalog, err := testCatalog(t, cfg, repo, cipher)
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
	catalog, err := testCatalog(t, enabledConfig(t), &fakeAuthorizationRepository{err: domain.ErrProviderNotFound}, &passthroughTokenCipher{})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	_, err = catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub)
	if !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

func TestCatalog_ObserveAccessMapsDecryptFailureToProviderUnavailable(t *testing.T) {
	catalog, err := testCatalog(t, enabledConfig(t), &fakeAuthorizationRepository{
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

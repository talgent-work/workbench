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

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

func TestCatalog_ObserveAccessUsesEveryUserInstallation(t *testing.T) {
	var sawRepositoryRequest bool
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
	catalog, err := NewCatalog(cfg)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	scope, err := catalog.ObserveAccess(context.Background(), "gho_user_catalog_token")
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

// GitHub refusing the user token fails the whole observation as a rejected
// credential, whichever request it refused; renewing is the Marketplace's.
func TestCatalog_ObserveAccessReportsARejectedToken(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	cfg := enabledConfig(t)
	cfg.APIBaseURL = server.URL
	catalog, err := NewCatalog(cfg)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if _, err := catalog.ObserveAccess(context.Background(), "gho_revoked"); !errors.Is(err, application.ErrCredentialRejected) {
		t.Fatalf("err=%v, want ErrCredentialRejected", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1: the adapter never retries", calls)
	}
	if _, err := catalog.ObserveAccess(context.Background(), ""); !errors.Is(err, application.ErrCredentialRejected) {
		t.Fatalf("err=%v, want ErrCredentialRejected without a token", err)
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

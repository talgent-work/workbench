package github

import (
	"context"
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
	catalog, err := testCatalog(t, cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
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
	catalog, err := testCatalog(t, cfg, &fakeAuthorizationRepository{}, mustTokenCipher(t))
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
	catalog, err := testCatalog(t, cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
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
	catalog, err := testCatalog(t, cfg, &fakeAuthorizationRepository{authorization: mustActiveAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour))}, cipher)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if _, err := catalog.ObserveAccess(context.Background(), "user-1", domain.ProviderGitHub); !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

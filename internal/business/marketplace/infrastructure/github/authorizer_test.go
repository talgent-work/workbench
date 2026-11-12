package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// Connecting always requests user authorization; which accounts it reaches is
// decided by the App installations the user manages on GitHub.
func TestAuthorizer_BeginProviderAuthorizationRequestsUserAuthorization(t *testing.T) {
	cfg := enabledConfig(t)
	cfg.WebBaseURL = "https://github.example.test"
	authorizer, err := NewAuthorizer(cfg, mustTokenCipher(t))
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	authorizer.now = func() time.Time { return time.Unix(100, 0).UTC() }

	resp, err := authorizer.BeginProviderAuthorization(context.Background(), application.BeginProviderAuthorizationInput{
		OwnerID:     "user-1",
		Provider:    domain.ProviderGitHub,
		RedirectURI: "https://app.example.test/marketplace/providers/github",
	})
	if err != nil {
		t.Fatalf("BeginProviderAuthorization: %v", err)
	}
	u, err := url.Parse(resp.AuthorizationURL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if u.Path != "/login/oauth/authorize" || u.Query().Get("client_id") != "client-id" || u.Query().Get("state") == "" ||
		u.Query().Get("redirect_uri") != "https://app.example.test/marketplace/providers/github" {
		t.Fatalf("authorization url=%q", resp.AuthorizationURL)
	}
}

func TestAuthorizer_CompleteProviderAuthorizationReturnsEncryptedAuthorization(t *testing.T) {
	var sawTokenExchange, sawUserFetch bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			sawTokenExchange = true
			if r.Method != http.MethodPost {
				t.Fatalf("oauth method=%s", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm: %v", err)
			}
			if r.Form.Get("client_id") != "client-id" || r.Form.Get("client_secret") != "client-secret" || r.Form.Get("code") != "code-1" {
				t.Fatalf("oauth form=%v", r.Form)
			}
			if r.Form.Get("redirect_uri") != "https://app.example.test/profile" {
				t.Fatalf("redirect_uri=%q", r.Form.Get("redirect_uri"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"ghu_user_token","refresh_token":"ghu_refresh_token","expires_in":3600,"refresh_token_expires_in":7200}`))
		case "/user":
			sawUserFetch = true
			if got := r.Header.Get("Authorization"); got != "Bearer ghu_user_token" {
				t.Fatalf("user Authorization=%q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":12345,"login":"octocat"}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	cipher := mustTokenCipher(t)
	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	cfg.APIBaseURL = server.URL
	authorizer, err := NewAuthorizer(cfg, cipher)
	if err != nil {
		t.Fatalf("NewAuthorizer: %v", err)
	}
	authorizer.now = func() time.Time { return time.Unix(100, 0).UTC() }
	begin, err := authorizer.BeginProviderAuthorization(context.Background(), application.BeginProviderAuthorizationInput{
		OwnerID:     "user-1",
		Provider:    domain.ProviderGitHub,
		RedirectURI: "https://app.example.test/profile",
	})
	if err != nil {
		t.Fatalf("BeginProviderAuthorization: %v", err)
	}
	state := stateFromURL(t, begin.AuthorizationURL)

	auth, err := authorizer.CompleteProviderAuthorization(context.Background(), application.CompleteProviderAuthorizationInput{
		OwnerID:     "user-1",
		Provider:    domain.ProviderGitHub,
		Code:        "code-1",
		State:       state,
		RedirectURI: "https://app.example.test/profile",
	})
	if err != nil {
		t.Fatalf("CompleteProviderAuthorization: %v", err)
	}
	if !sawTokenExchange || !sawUserFetch {
		t.Fatalf("missing expected calls token=%v user=%v", sawTokenExchange, sawUserFetch)
	}
	plaintext, err := cipher.Decrypt(auth.AccessTokenCiphertext)
	if err != nil {
		t.Fatalf("Decrypt access token: %v", err)
	}
	if auth.OwnerID != "user-1" || auth.ProviderLogin != "octocat" || plaintext != "ghu_user_token" {
		t.Fatalf("authorization=%+v plaintext=%q", auth, plaintext)
	}
	if !auth.RefreshTokenExpiresAt.Equal(time.Unix(100, 0).UTC().Add(2 * time.Hour)) {
		t.Fatalf("RefreshTokenExpiresAt=%s", auth.RefreshTokenExpiresAt)
	}
}

func stateFromURL(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	state := u.Query().Get("state")
	if state == "" {
		t.Fatalf("missing state in %q", raw)
	}
	return state
}

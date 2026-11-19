package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
)

// GitHub renews with the refresh token in the form and rotates both
// credentials, each with its stated expiry.
func TestAuthorizationPort_RenewsAtGitHub(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cipher := mustTokenCipher(t)
	accessCiphertext, err := cipher.Encrypt("expired-access-token")
	if err != nil {
		t.Fatalf("Encrypt access: %v", err)
	}
	refreshCiphertext, err := cipher.Encrypt("refresh-token")
	if err != nil {
		t.Fatalf("Encrypt refresh: %v", err)
	}
	auth := mustAuthorization(t, "user-1", accessCiphertext, now.Add(-time.Minute))
	auth.RefreshTokenCiphertext = refreshCiphertext

	var sawRefresh bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
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
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	port := &AuthorizationPort{cfg: cfg, http: server.Client(), cipher: cipher, now: func() time.Time { return now }}
	renewal, err := port.Renew(context.Background(), auth.CredentialSet)
	if err != nil || renewal.ReconnectRequired {
		t.Fatalf("Renew: %+v %v", renewal, err)
	}
	if !sawRefresh {
		t.Fatal("missing refresh token request")
	}
	renewed := renewal.Credentials
	if !renewed.ExpiresAt.Equal(now.Add(time.Hour)) || !renewed.RefreshTokenExpiresAt.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("expiries=%s/%s", renewed.ExpiresAt, renewed.RefreshTokenExpiresAt)
	}
	for ciphertext, want := range map[string]string{renewed.AccessTokenCiphertext: "fresh-access-token", renewed.RefreshTokenCiphertext: "fresh-refresh-token"} {
		if plaintext, err := cipher.Decrypt(ciphertext); err != nil || plaintext != want {
			t.Fatalf("renewed credential was not the expected test value")
		}
	}
}

func TestTokenCipherRoundTripWithVersionPrefix(t *testing.T) {
	cipher := mustTokenCipher(t)
	ciphertext, err := cipher.Encrypt("secret-token-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ciphertext == "secret-token-value" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	if got := ciphertext[:len("v1:")]; got != "v1:" {
		t.Fatalf("ciphertext prefix=%q", got)
	}
	plaintext, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if plaintext != "secret-token-value" {
		t.Fatalf("plaintext=%q", plaintext)
	}
}

func mustTokenCipher(t *testing.T) TokenCipher {
	t.Helper()
	cipher, err := credentialcipher.New("test cipher key")
	if err != nil {
		t.Fatalf("credentialcipher.New: %v", err)
	}
	return cipher
}

func enabledConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Enabled:           true,
		AppID:             "123",
		AppSlug:           "talgent",
		PrivateKeyPEM:     testPrivateKeyPEM(t),
		OAuthClientID:     "client-id",
		OAuthClientSecret: "client-secret",
		WebBaseURL:        "https://github.com",
		TokenCipherKey:    "test cipher key",
	}
}

func mustAuthorization(t *testing.T, ownerID, accessTokenCiphertext string, expiresAt time.Time) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.RehydrateProviderAuthorization(domain.ProviderAuthorization{
		Status:   domain.ProviderAuthorizationActive,
		ID:       "authorization-1",
		OwnerID:  ownerID,
		Provider: domain.ProviderGitHub,
		Version:  1,

		ExternalAccount: domain.ExternalAccount{
			ProviderAccountID: "12345",
			ProviderLogin:     "octocat",
		},

		CredentialSet: domain.CredentialSet{
			AccessTokenCiphertext:  accessTokenCiphertext,
			RefreshTokenCiphertext: "refresh-token-ciphertext",
			ExpiresAt:              expiresAt,
		},
	})
	if err != nil {
		t.Fatalf("authorization: %v", err)
	}
	return auth
}

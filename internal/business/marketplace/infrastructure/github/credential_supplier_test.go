package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestCredentialSupplier_SupplyRepositoryCredentialUsesUserAuthorizationExpiry(t *testing.T) {
	expiresAt := time.Now().UTC().Add(30 * time.Minute)
	repo := &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", "ciphertext", expiresAt),
	}
	cipher := &passthroughTokenCipher{}
	issuer, err := NewCredentialSupplier(enabledConfig(t), repo, cipher)
	if err != nil {
		t.Fatalf("NewCredentialSupplier: %v", err)
	}

	issued, err := issuer.SupplyRepositoryCredential(context.Background(), "user-1", "authorization-1", "https://github.com/acme/api.git")
	if err != nil {
		t.Fatalf("SupplyRepositoryCredential: %v", err)
	}
	if !issued.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt=%s, want auth expiry %s", issued.ExpiresAt, expiresAt)
	}
	if cipher.decryptCalls != 1 {
		t.Fatalf("SupplyRepositoryCredential must decrypt only the supplied token, decrypt calls=%d", cipher.decryptCalls)
	}
}

// Supply renews an expired authorization before returning usable material.
func TestCredentialSupplier_SupplyRepositoryCredentialRefreshesExpiredUserAuthorization(t *testing.T) {
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
	repo := &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", accessCiphertext, now.Add(-time.Minute)),
	}
	repo.authorization.RefreshTokenCiphertext = refreshCiphertext

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
	issuer, err := NewCredentialSupplier(cfg, repo, cipher)
	if err != nil {
		t.Fatalf("NewCredentialSupplier: %v", err)
	}
	issuer.now = func() time.Time { return now }
	issuer.http = server.Client()

	issued, err := issuer.SupplyRepositoryCredential(context.Background(), "user-1", "authorization-1", server.URL+"/acme/api.git")
	if err != nil {
		t.Fatalf("SupplyRepositoryCredential: %v", err)
	}
	if !sawRefresh {
		t.Fatal("missing refresh token request")
	}
	if !issued.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt=%s, want %s", issued.ExpiresAt, now.Add(time.Hour))
	}
	if repo.saveCalls != 1 {
		t.Fatalf("saveCalls=%d, want 1", repo.saveCalls)
	}
	accessToken, err := cipher.Decrypt(repo.authorization.AccessTokenCiphertext)
	if err != nil {
		t.Fatalf("Decrypt saved access: %v", err)
	}
	if accessToken != "fresh-access-token" {
		t.Fatalf("saved access token was not the expected test value")
	}
	refreshToken, err := cipher.Decrypt(repo.authorization.RefreshTokenCiphertext)
	if err != nil {
		t.Fatalf("Decrypt saved refresh: %v", err)
	}
	if refreshToken != "fresh-refresh-token" {
		t.Fatalf("saved refresh token was not the expected test value")
	}
}

// Intent (real): an expired GitHub authorization without a refresh token must
// still fail closed instead of issuing checkout credentials from stale state.
func TestCredentialSupplier_SupplyRepositoryCredentialRejectsExpiredAuthorizationWithoutRefreshToken(t *testing.T) {
	auth := mustAuthorization(t, "user-1", "ciphertext", time.Now().UTC().Add(-time.Minute))
	auth.RefreshTokenCiphertext = ""
	issuer, err := NewCredentialSupplier(enabledConfig(t), &fakeAuthorizationRepository{authorization: auth}, &passthroughTokenCipher{})
	if err != nil {
		t.Fatalf("NewCredentialSupplier: %v", err)
	}

	_, err = issuer.SupplyRepositoryCredential(context.Background(), "user-1", "authorization-1", "https://github.com/acme/api.git")
	if !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
	}
}

func TestCredentialSupplier_SupplyRepositoryCredentialUsesUserOAuthAccessToken(t *testing.T) {
	cipher := mustTokenCipher(t)
	ciphertext, err := cipher.Encrypt("gho_user_access_token")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	repo := &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", ciphertext, time.Now().UTC().Add(time.Hour)),
	}
	issuer, err := NewCredentialSupplier(enabledConfig(t), repo, cipher)
	if err != nil {
		t.Fatalf("NewCredentialSupplier: %v", err)
	}

	credential, err := issuer.SupplyRepositoryCredential(context.Background(), "user-1", "authorization-1", "https://github.com/acme/api.git")
	if err != nil {
		t.Fatalf("SupplyRepositoryCredential: %v", err)
	}
	if credential.Username != "x-access-token" {
		t.Fatalf("Username=%q", credential.Username)
	}
	if credential.Password != "gho_user_access_token" {
		t.Fatalf("Password=%q", credential.Password)
	}
}

func TestCredentialSupplier_MapsMissingAuthorizationToProviderUnavailable(t *testing.T) {
	issuer, err := NewCredentialSupplier(enabledConfig(t), &fakeAuthorizationRepository{err: domain.ErrProviderNotFound}, &passthroughTokenCipher{})
	if err != nil {
		t.Fatalf("NewCredentialSupplier: %v", err)
	}
	_, err = issuer.SupplyRepositoryCredential(context.Background(), "user-1", "authorization-1", "https://github.com/acme/api.git")
	if !errors.Is(err, application.ErrProviderUnavailable) {
		t.Fatalf("err=%v, want ErrProviderUnavailable", err)
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
	cipher, err := NewTokenCipher(Config{TokenCipherKey: "test cipher key"})
	if err != nil {
		t.Fatalf("NewTokenCipher: %v", err)
	}
	return cipher
}

func TestTokenCipherCanConstructWhenGitHubProviderDisabled(t *testing.T) {
	if _, err := NewTokenCipher(Config{Enabled: "false"}); err != nil {
		t.Fatalf("NewTokenCipher disabled: %v", err)
	}
}

func enabledConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Enabled:           "true",
		AppID:             "123",
		AppSlug:           "talgent",
		PrivateKeyPEM:     testPrivateKeyPEM(t),
		OAuthClientID:     "client-id",
		OAuthClientSecret: "client-secret",
		WebBaseURL:        "https://github.com",
		TokenCipherKey:    "test cipher key",
	}
}

type fakeAuthorizationRepository struct {
	authorization       *domain.ProviderAuthorization
	reloadAuthorization *domain.ProviderAuthorization
	err                 error
	acquireErr          error
	saveCalls           int
	acquireCalls        int
}

func (r *fakeAuthorizationRepository) Save(_ context.Context, auth *domain.ProviderAuthorization) error {
	r.saveCalls++
	r.authorization = cloneAuthorization(auth)
	return nil
}

func (r *fakeAuthorizationRepository) Get(context.Context, string) (*domain.ProviderAuthorization, error) {
	if r.err != nil {
		return nil, r.err
	}
	if r.reloadAuthorization != nil {
		return cloneAuthorization(r.reloadAuthorization), nil
	}
	return cloneAuthorization(r.authorization), nil
}

func (r *fakeAuthorizationRepository) GetByOwnerAndProvider(context.Context, string, domain.Provider) (*domain.ProviderAuthorization, error) {
	if r.err != nil {
		return nil, r.err
	}
	return cloneAuthorization(r.authorization), nil
}

func (r *fakeAuthorizationRepository) AcquireCredentialLease(_ context.Context, auth *domain.ProviderAuthorization, leaseID string, leaseUntil, now time.Time) (*domain.ProviderAuthorization, error) {
	r.acquireCalls++
	if r.acquireErr != nil {
		return nil, r.acquireErr
	}
	next := cloneAuthorization(auth)
	next.Version++
	r.authorization = cloneAuthorization(next)
	return cloneAuthorization(next), nil
}

func cloneAuthorization(auth *domain.ProviderAuthorization) *domain.ProviderAuthorization {
	if auth == nil {
		return nil
	}
	copy := *auth
	return &copy
}

type passthroughTokenCipher struct {
	decryptCalls int
}

func (c *passthroughTokenCipher) Encrypt(plaintext string) (string, error) {
	return plaintext, nil
}

func (c *passthroughTokenCipher) Decrypt(ciphertext string) (string, error) {
	c.decryptCalls++
	return ciphertext, nil
}

type failingEncryptCipher struct{}

func (failingEncryptCipher) Encrypt(string) (string, error) {
	return "", errors.New("encrypt failed")
}

func (failingEncryptCipher) Decrypt(ciphertext string) (string, error) {
	return ciphertext, nil
}

func mustAuthorization(t *testing.T, ownerUserID, accessTokenCiphertext string, expiresAt time.Time) *domain.ProviderAuthorization {
	t.Helper()
	return mustActiveAuthorization(t, ownerUserID, accessTokenCiphertext, expiresAt)
}

func mustActiveAuthorization(t *testing.T, ownerUserID, accessTokenCiphertext string, expiresAt time.Time) *domain.ProviderAuthorization {
	t.Helper()
	auth, err := domain.RehydrateProviderAuthorization(domain.ProviderAuthorization{
		Status:      domain.ProviderAuthorizationActive,
		ID:          "authorization-1",
		OwnerUserID: ownerUserID,
		Provider:    domain.ProviderGitHub,
		Version:     1,

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

// The declared expiry is the credential's own at GitHub, however distant; a
// credential that does not expire declares none.
func TestDeclaredExpiryIsTheCredentialsOwn(t *testing.T) {
	now := time.Now().UTC()
	supplier := &CredentialSupplier{now: func() time.Time { return now }}
	expiring := &domain.ProviderAuthorization{CredentialSet: domain.CredentialSet{ExpiresAt: now.Add(8 * time.Hour)}}
	declared, err := supplier.runtimeExpiry(expiring)
	if err != nil || !declared.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("declared=%s err=%v, want the credential's expiry", declared, err)
	}
	declared, err = supplier.runtimeExpiry(&domain.ProviderAuthorization{})
	if err != nil || !declared.IsZero() {
		t.Fatalf("declared=%s err=%v, want no expiry", declared, err)
	}
	if _, err := supplier.runtimeExpiry(&domain.ProviderAuthorization{CredentialSet: domain.CredentialSet{ExpiresAt: now.Add(-time.Second)}}); err == nil {
		t.Fatal("an expired credential is not supplied")
	}
}

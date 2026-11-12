package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

func TestFreshUserTokenManager_ConcurrentReloadCannotChangeAuthorizationOwner(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeAuthorizationRepository{
		authorization:       mustAuthorization(t, "user-1", "expired-ciphertext", now.Add(-time.Minute)),
		reloadAuthorization: mustAuthorization(t, "user-2", "foreign-ciphertext", now.Add(time.Hour)),
		acquireErr:          domain.ErrConcurrentModification,
	}
	manager := newFreshUserTokenManager(enabledConfig(t), http.DefaultClient, repo, &passthroughTokenCipher{}, func() time.Time { return now })
	_, token, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	if !errors.Is(err, domain.ErrPermissionDenied) || token != "" {
		t.Fatalf("concurrent recovery must reject foreign ownership without returning credentials: %v", err)
	}
}

// Intent (real): a near-expiry GitHub user token must be refreshed under a DB
// lease and persist the rotated access/refresh pair with both expiry values.
func TestFreshUserTokenManager_RefreshesNearExpiryAuthorizationWithLease(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	cipher := mustTokenCipher(t)
	accessCiphertext, err := cipher.Encrypt("old-access-token")
	if err != nil {
		t.Fatalf("Encrypt access: %v", err)
	}
	refreshCiphertext, err := cipher.Encrypt("old-refresh-token")
	if err != nil {
		t.Fatalf("Encrypt refresh: %v", err)
	}
	auth := mustAuthorization(t, "user-1", accessCiphertext, now.Add(2*time.Minute))
	auth.RefreshTokenCiphertext = refreshCiphertext
	auth.RefreshTokenExpiresAt = now.Add(24 * time.Hour)
	repo := &fakeAuthorizationRepository{authorization: auth}

	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/oauth/access_token" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		if got := r.Form.Get("refresh_token"); got != "old-refresh-token" {
			t.Fatalf("refresh token form value was not the expected decrypted test value")
		}
		sawRefresh = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","refresh_token":"new-refresh-token","expires_in":3600,"refresh_token_expires_in":7200}`))
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	manager := newFreshUserTokenManager(cfg, server.Client(), repo, cipher, func() time.Time { return now })

	refreshed, token, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{
		OwnerID:  "user-1",
		Provider: domain.ProviderGitHub,
		Reason:   "test",
	})
	if err != nil {
		t.Fatalf("EnsureFreshUserToken: %v", err)
	}
	if !sawRefresh {
		t.Fatal("missing OAuth refresh request")
	}
	if repo.acquireCalls != 1 || repo.saveCalls != 1 {
		t.Fatalf("acquire/save calls=%d/%d, want 1/1", repo.acquireCalls, repo.saveCalls)
	}
	if token != "new-access-token" {
		t.Fatalf("returned token was not the expected rotated test value")
	}
	if !refreshed.ExpiresAt.Equal(now.Add(time.Hour)) || !refreshed.RefreshTokenExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("expiries=%s/%s", refreshed.ExpiresAt, refreshed.RefreshTokenExpiresAt)
	}
	accessToken, err := cipher.Decrypt(repo.authorization.AccessTokenCiphertext)
	if err != nil {
		t.Fatalf("Decrypt saved access: %v", err)
	}
	refreshToken, err := cipher.Decrypt(repo.authorization.RefreshTokenCiphertext)
	if err != nil {
		t.Fatalf("Decrypt saved refresh: %v", err)
	}
	if accessToken != "new-access-token" || refreshToken != "new-refresh-token" {
		t.Fatalf("rotated token pair not persisted")
	}
}

// Intent (real): a non-expired GitHub user token outside the request skew
// should be reused without taking a refresh lease or contacting GitHub.
func TestFreshUserTokenManager_UsesValidTokenWithoutRefresh(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	repo := &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", "valid-access-token", now.Add(time.Hour)),
	}
	manager := newFreshUserTokenManager(enabledConfig(t), http.DefaultClient, repo, &passthroughTokenCipher{}, func() time.Time { return now })

	auth, token, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	if err != nil {
		t.Fatalf("EnsureFreshUserToken: %v", err)
	}
	if auth.ID != "authorization-1" || token != "valid-access-token" {
		t.Fatalf("auth/token=%s/%q, want existing authorization and access token", auth.ID, token)
	}
	if repo.acquireCalls != 0 || repo.saveCalls != 0 {
		t.Fatalf("acquire/save calls=%d/%d, want no refresh path", repo.acquireCalls, repo.saveCalls)
	}
}

// Intent (real): a missing refresh token means the provider authorization must
// require reconnect and stale access credentials must not be returned.
func TestFreshUserTokenManager_MissingRefreshTokenMarksReconnectRequired(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := mustAuthorization(t, "user-1", "access-ciphertext", now.Add(-time.Minute))
	auth.RefreshTokenCiphertext = ""
	repo := &fakeAuthorizationRepository{authorization: auth}
	manager := newFreshUserTokenManager(enabledConfig(t), http.DefaultClient, repo, &passthroughTokenCipher{}, func() time.Time { return now })

	_, _, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{
		OwnerID:  "user-1",
		Provider: domain.ProviderGitHub,
	})
	var providerErr *application.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != providerErrorGitHubReconnectRequired {
		t.Fatalf("err=%v, want reconnect provider error", err)
	}
	if repo.saveCalls != 1 || repo.authorization.Status != domain.ProviderAuthorizationReconnectRequired {
		t.Fatalf("save/status=%d/%s, want reconnect_required saved", repo.saveCalls, repo.authorization.Status)
	}
}

// Intent (real): after GitHub has rotated the refresh token, local storage
// failures must move the connection to reconnect_required rather than retrying
// with the now-invalid old token.
func TestFreshUserTokenManager_EncryptFailureAfterRotationMarksReconnectRequired(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := mustAuthorization(t, "user-1", "old-access", now.Add(-time.Minute))
	auth.RefreshTokenCiphertext = "old-refresh"
	auth.RefreshTokenExpiresAt = now.Add(time.Hour)
	repo := &fakeAuthorizationRepository{authorization: auth}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"new-access-token","refresh_token":"new-refresh-token","expires_in":3600,"refresh_token_expires_in":7200}`))
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	manager := newFreshUserTokenManager(cfg, server.Client(), repo, failingEncryptCipher{}, func() time.Time { return now })

	_, _, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	var providerErr *application.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != providerErrorGitHubReconnectRequired {
		t.Fatalf("err=%v, want reconnect provider error", err)
	}
	if repo.authorization.Status != domain.ProviderAuthorizationReconnectRequired {
		t.Fatalf("authorization status=%s, want reconnect_required", repo.authorization.Status)
	}
}

// A rejected refresh token requires OAuth again, even when GitHub returns HTTP
// 200. Persist that state so later consumers cannot keep retrying the old token.
func TestFreshUserTokenManager_RejectedRefreshTokenRequiresReauthorization(t *testing.T) {
	for _, test := range []struct {
		code   string
		status int
	}{
		{"bad_refresh_token", http.StatusOK},
		{"bad_refresh_token", http.StatusBadRequest},
		{"invalid_grant", http.StatusOK},
	} {
		t.Run(fmt.Sprintf("%s/%d", test.code, test.status), func(t *testing.T) {
			now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
			auth := mustAuthorization(t, "user-1", "access-ciphertext", now.Add(-time.Minute))
			auth.RefreshTokenExpiresAt = now.Add(time.Hour)
			repo := &fakeAuthorizationRepository{authorization: auth}
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(test.status)
				_, _ = fmt.Fprintf(w, `{"error":%q,"error_description":"refresh token expired"}`, test.code)
			}))
			defer server.Close()

			cfg := enabledConfig(t)
			cfg.WebBaseURL = server.URL
			manager := newFreshUserTokenManager(cfg, server.Client(), repo, &passthroughTokenCipher{}, func() time.Time { return now })

			for range 2 {
				_, token, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
				var providerErr *application.ProviderError
				require.ErrorAs(t, err, &providerErr)
				require.Equal(t, providerErrorGitHubReconnectRequired, providerErr.Code)
				require.False(t, providerErr.Retryable)
				require.Empty(t, token, "expired credentials must never be returned")
			}
			require.Equal(t, domain.ProviderAuthorizationReconnectRequired, repo.authorization.Status)
			require.Equal(t, string(providerErrorGitHubReconnectRequired), repo.authorization.LastFailureCode)
			require.EqualValues(t, 1, requests.Load(), "later consumers must not retry the rejected refresh token")
		})
	}
}

// Intent (real): when another request holds the refresh lease, callers should
// reload a winner if it already refreshed, otherwise return a retryable in-progress error.
func TestFreshUserTokenManager_ConcurrentLeaseReloadsWinnerOrReturnsInProgress(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	repo := &fakeAuthorizationRepository{
		authorization: mustAuthorization(t, "user-1", "fresh-access", now.Add(time.Hour)),
		acquireErr:    domain.ErrConcurrentModification,
	}
	stale := cloneAuthorization(repo.authorization)
	stale.ExpiresAt = now.Add(-time.Minute)
	repo.authorization = stale
	manager := newFreshUserTokenManager(enabledConfig(t), http.DefaultClient, repo, &passthroughTokenCipher{}, func() time.Time { return now })

	repo.reloadAuthorization = mustAuthorization(t, "user-1", "winner-access", now.Add(time.Hour))
	auth, token, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	if err != nil {
		t.Fatalf("EnsureFreshUserToken winner: %v", err)
	}
	if token != "winner-access" || auth.ExpiresAt.Before(now.Add(59*time.Minute)) {
		t.Fatalf("winner token not returned")
	}

	repo.authorization = stale
	repo.reloadAuthorization = nil
	_, _, err = manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	var providerErr *application.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != providerErrorGitHubRefreshInProgress || !providerErr.Retryable {
		t.Fatalf("err=%v, want retryable refresh-in-progress", err)
	}
}

// Intent (real): transient GitHub refresh failures should record a retryable
// provider failure and clear any refresh lease for a later attempt.
func TestFreshUserTokenManager_TransientRefreshFailureRecordsFailureAndClearsLease(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	auth := mustAuthorization(t, "user-1", "access-ciphertext", now.Add(-time.Minute))
	auth.RefreshTokenExpiresAt = now.Add(time.Hour)
	repo := &fakeAuthorizationRepository{authorization: auth}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "try later", http.StatusBadGateway)
	}))
	defer server.Close()

	cfg := enabledConfig(t)
	cfg.WebBaseURL = server.URL
	manager := newFreshUserTokenManager(cfg, server.Client(), repo, &passthroughTokenCipher{}, func() time.Time { return now })

	_, _, err := manager.EnsureFreshUserToken(context.Background(), EnsureFreshUserTokenInput{OwnerID: "user-1", Provider: domain.ProviderGitHub})
	var providerErr *application.ProviderError
	if !errors.As(err, &providerErr) || providerErr.Code != providerErrorGitHubProviderTransient || !providerErr.Retryable {
		t.Fatalf("err=%v, want retryable transient provider error", err)
	}
	if repo.saveCalls != 1 || repo.authorization.LastFailureCode != string(providerErrorGitHubProviderTransient) {
		t.Fatalf("failure not recorded: save=%d code=%q", repo.saveCalls, repo.authorization.LastFailureCode)
	}
}

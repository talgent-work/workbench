package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// Exercise the actual aggregate and HTTP adapter together: cancellation must
// revoke the app authorization, rather than merely deleting a local token.
func TestAuthorizationPort_CancelsGitHubAppGrant(t *testing.T) {
	for _, status := range []int{204, 404, 422, 429, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				user, password, ok := r.BasicAuth()
				if !ok || user != "test-client" || password != "test-client-secret" {
					t.Error("request must use configured client authentication")
				}
				if r.Method != http.MethodDelete || r.URL.Path != "/applications/test-client/grant" {
					t.Error("request must delete the app authorization")
				}
				var payload struct {
					AccessToken string `json:"access_token"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.AccessToken != "test-access-secret" {
					t.Error("request must contain the decrypted access credential")
				}
				w.WriteHeader(status)
				if status != http.StatusNoContent {
					_, _ = w.Write([]byte(`{"message":"test-access-secret test-client-secret"}`))
				}
			}))
			defer server.Close()
			port, auth := revocationFixture(t, server)
			err := auth.CancelAuthorization(context.Background(), auth.OwnerUserID, time.Now(), port)
			require.EqualValues(t, 1, calls.Load())
			if status == http.StatusNoContent {
				require.NoError(t, err)
				require.Equal(t, domain.ProviderAuthorizationRevoked, auth.Status)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "test-access-secret")
				require.NotContains(t, err.Error(), "test-client-secret")
				require.Equal(t, domain.ProviderAuthorizationActive, auth.Status)
			}
		})
	}
}

func TestAuthorizationPort_RevocationNeverFollowsRedirect(t *testing.T) {
	var leaked atomic.Bool
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		leaked.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer redirect.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirect.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	port, auth := revocationFixture(t, server)
	err := auth.CancelAuthorization(context.Background(), auth.OwnerUserID, time.Now(), port)
	require.Error(t, err)
	require.False(t, leaked.Load())
	require.Equal(t, domain.ProviderAuthorizationActive, auth.Status)
}

func TestAuthorizationPort_ExpiredAccessRenewsBeforeCancellation(t *testing.T) {
	var renewal, revocation atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			renewal.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"rotated-access","refresh_token":"rotated-refresh","expires_in":3600,"refresh_token_expires_in":7200}`))
		case "/applications/test-client/grant":
			revocation.Add(1)
			var body struct {
				AccessToken string `json:"access_token"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AccessToken != "rotated-access" {
				t.Error("revocation must use the renewed access credential")
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Error("unexpected Provider operation")
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	port, auth := revocationFixture(t, server)
	auth.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, auth.CancelAuthorization(context.Background(), auth.OwnerUserID, time.Now(), port))
	require.EqualValues(t, 1, renewal.Load())
	require.EqualValues(t, 1, revocation.Load())
	require.Equal(t, domain.ProviderAuthorizationRevoked, auth.Status)
}

func revocationFixture(t *testing.T, server *httptest.Server) (*AuthorizationPort, *domain.ProviderAuthorization) {
	t.Helper()
	cipher := mustTokenCipher(t)
	access, err := cipher.Encrypt("test-access-secret")
	require.NoError(t, err)
	refresh, err := cipher.Encrypt("test-refresh-secret")
	require.NoError(t, err)
	require.False(t, strings.Contains(access, "test-access-secret"))
	auth := mustAuthorization(t, "user-1", access, time.Now().Add(time.Hour))
	auth.RefreshTokenCiphertext = refresh
	auth.RefreshTokenExpiresAt = time.Now().Add(24 * time.Hour)
	cfg := enabledConfig(t)
	cfg.OAuthClientID, cfg.OAuthClientSecret = "test-client", "test-client-secret"
	cfg.APIBaseURL, cfg.WebBaseURL = server.URL, server.URL
	port, err := NewAuthorizationPort(cfg, cipher)
	require.NoError(t, err)
	port.http = server.Client()
	return port, auth
}

package oauthprovider

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
)

// authorizationServer answers the token and revocation endpoints as told and
// records every form it was sent.
type authorizationServer struct {
	mu        sync.Mutex
	forms     []url.Values
	basicAuth []string
	status    int
	answer    map[string]any
}

func (s *authorizationServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	user, password, _ := r.BasicAuth()
	s.mu.Lock()
	s.forms = append(s.forms, r.PostForm)
	s.basicAuth = append(s.basicAuth, user+":"+password)
	status, answer := s.status, s.answer
	s.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *authorizationServer) last() url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.forms[len(s.forms)-1]
}

func fixture(t *testing.T) (*authorizationServer, Client, application.CredentialCipher) {
	t.Helper()
	server := &authorizationServer{}
	httpServer := httptest.NewServer(server)
	t.Cleanup(httpServer.Close)
	cipher, err := credentialcipher.New("oauth provider test key")
	require.NoError(t, err)
	client := Client{Provider: "example-sso", ClientID: "workbench", ClientSecret: "secret", Scopes: []string{"openid", "offline_access"},
		Resources:             []string{"https://docs.example.com", "https://tickets.example.com"},
		AuthorizationEndpoint: "https://sso.example.com/authorize", TokenEndpoint: httpServer.URL + "/token", RevocationEndpoint: httpServer.URL + "/revoke"}
	return server, client, cipher
}

func idToken(claims map[string]string) string {
	payload, _ := json.Marshal(claims)
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

// The person's code is exchanged with the verifier whose challenge the
// authorization address carried, for the owner who began, at the redirect
// they began with; any other completion is refused.
func TestAuthorizationCodeFlowWithPKCE(t *testing.T) {
	server, client, cipher := fixture(t)
	authorizer, err := NewAuthorizer([]Client{client}, cipher, nil)
	require.NoError(t, err)
	begin := application.BeginProviderAuthorizationInput{OwnerID: "user-1", Provider: "example-sso", RedirectURI: "https://workbench.example.com/profile"}
	begun, err := authorizer.BeginProviderAuthorization(t.Context(), begin)
	require.NoError(t, err)
	address, err := url.Parse(begun.AuthorizationURL)
	require.NoError(t, err)
	query := address.Query()
	require.Equal(t, "sso.example.com", address.Host)
	require.Equal(t, "S256", query.Get("code_challenge_method"))
	require.Equal(t, "openid offline_access", query.Get("scope"))
	require.Equal(t, []string{"https://docs.example.com", "https://tickets.example.com"}, query["resource"], "every target's resource lies within the grant")

	server.answer = map[string]any{"access_token": "access", "refresh_token": "refresh", "expires_in": 300, "refresh_expires_in": 1800,
		"id_token": idToken(map[string]string{"sub": "subject-1", "preferred_username": "alice"})}
	complete := application.CompleteProviderAuthorizationInput{OwnerID: "user-1", Provider: "example-sso", Code: "code", State: query.Get("state"), RedirectURI: begin.RedirectURI}
	authorization, err := authorizer.CompleteProviderAuthorization(t.Context(), complete)
	require.NoError(t, err)
	exchanged := server.last()
	challenge := sha256.Sum256([]byte(exchanged.Get("code_verifier")))
	require.Equal(t, query.Get("code_challenge"), base64.RawURLEncoding.EncodeToString(challenge[:]))
	require.Equal(t, "authorization_code", exchanged.Get("grant_type"))
	require.Equal(t, "workbench:secret", server.basicAuth[0])
	require.Equal(t, "subject-1", authorization.ProviderAccountID)
	require.Equal(t, "alice", authorization.ProviderLogin)
	require.NotZero(t, authorization.RefreshTokenExpiresAt)
	plaintext, err := cipher.Decrypt(authorization.RefreshTokenCiphertext)
	require.NoError(t, err)
	require.Equal(t, "refresh", plaintext)

	for name, change := range map[string]func(*application.CompleteProviderAuthorizationInput){
		"another owner": func(c *application.CompleteProviderAuthorizationInput) { c.OwnerID = "user-2" },
		"another redirect": func(c *application.CompleteProviderAuthorizationInput) {
			c.RedirectURI = "https://elsewhere.example.com"
		},
		"forged state": func(c *application.CompleteProviderAuthorizationInput) { c.State = "forged" },
	} {
		changed := complete
		change(&changed)
		_, err = authorizer.CompleteProviderAuthorization(t.Context(), changed)
		require.ErrorIs(t, err, domain.ErrPermissionDenied, name)
	}
	authorizer.now = func() time.Time { return time.Now().Add(stateLifetime + time.Minute) }
	_, err = authorizer.CompleteProviderAuthorization(t.Context(), complete)
	require.ErrorIs(t, err, domain.ErrPermissionDenied, "an expired state")
}

// Renewal keeps a refresh credential the server did not rotate; a refused
// grant needs the owner to reconnect, an unreachable server is retryable.
func TestRenewal(t *testing.T) {
	server, client, cipher := fixture(t)
	port, err := NewPort(client, cipher, nil)
	require.NoError(t, err)
	refresh, err := cipher.Encrypt("refresh")
	require.NoError(t, err)
	current := domain.CredentialSet{RefreshTokenCiphertext: refresh}

	server.answer = map[string]any{"access_token": "renewed", "expires_in": 300}
	renewal, err := port.Renew(t.Context(), current)
	require.NoError(t, err)
	require.Equal(t, refresh, renewal.Credentials.RefreshTokenCiphertext)
	require.Empty(t, server.last().Get("resource"))

	server.status, server.answer = http.StatusBadRequest, map[string]any{"error": "invalid_grant"}
	renewal, err = port.Renew(t.Context(), current)
	require.NoError(t, err)
	require.True(t, renewal.ReconnectRequired)

	server.status, server.answer = http.StatusServiceUnavailable, map[string]any{}
	_, err = port.Renew(t.Context(), current)
	var providerErr *application.ProviderError
	require.ErrorAs(t, err, &providerErr)
	require.True(t, providerErr.Retryable)
}

// A resource-indicator target's credential is requested for its resource,
// and a rotated refresh credential is returned for the authorization.
func TestTargetCredentialByResourceIndicator(t *testing.T) {
	server, client, cipher := fixture(t)
	port, err := NewPort(client, cipher, nil)
	require.NoError(t, err)
	refresh, err := cipher.Encrypt("refresh")
	require.NoError(t, err)
	server.answer = map[string]any{"access_token": "for-docs", "refresh_token": "rotated", "expires_in": 300}
	outcome, err := port.ObtainTargetCredential(t.Context(), domain.CredentialSet{RefreshTokenCiphertext: refresh},
		domain.ProviderTarget{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}, Resource: "https://docs.example.com"})
	require.NoError(t, err)
	require.Equal(t, "https://docs.example.com", server.last().Get("resource"))
	require.Equal(t, "refresh_token", server.last().Get("grant_type"))
	token, err := cipher.Decrypt(outcome.Credential.TokenCiphertext)
	require.NoError(t, err)
	require.Equal(t, "for-docs", token)
	require.NotEmpty(t, outcome.RefreshTokenCiphertext)

	_, err = port.ObtainTargetCredential(t.Context(), domain.CredentialSet{RefreshTokenCiphertext: refresh}, domain.ProviderTarget{Name: "shared"})
	require.ErrorIs(t, err, domain.ErrCredentialTargetInapplicable, "a target without a resource takes the shared credential")
}

// Revocation sends the refresh credential to the declared endpoint; only an
// answered 200 confirms it.
// Client secrets and tokens travel over HTTPS, or over HTTP to this machine
// alone.
func TestEndpointsAreHTTPS(t *testing.T) {
	_, client, _ := fixture(t)
	require.NoError(t, client.Validate(), "a loopback test server")
	client.TokenEndpoint = "http://sso.example.com/token"
	require.Error(t, client.Validate())
}

func TestRevocation(t *testing.T) {
	server, client, cipher := fixture(t)
	port, err := NewPort(client, cipher, nil)
	require.NoError(t, err)
	refresh, err := cipher.Encrypt("refresh")
	require.NoError(t, err)
	confirmed, err := port.Revoke(t.Context(), domain.CredentialSet{RefreshTokenCiphertext: refresh})
	require.NoError(t, err)
	require.True(t, confirmed)
	require.Equal(t, url.Values{"token": {"refresh"}, "token_type_hint": {"refresh_token"}}, server.last())

	server.status = http.StatusServiceUnavailable
	_, err = port.Revoke(t.Context(), domain.CredentialSet{RefreshTokenCiphertext: refresh})
	require.ErrorIs(t, err, domain.ErrRevocationUnconfirmed)

	client.RevocationEndpoint = ""
	withoutEndpoint, err := NewPort(client, cipher, nil)
	require.NoError(t, err)
	_, err = withoutEndpoint.Revoke(t.Context(), domain.CredentialSet{RefreshTokenCiphertext: refresh})
	require.ErrorIs(t, err, domain.ErrRevocationUnconfirmed)
}

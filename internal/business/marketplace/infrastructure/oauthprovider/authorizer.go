package oauthprovider

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// stateLifetime bounds how long a person may take at the Provider.
const stateLifetime = 15 * time.Minute

// Authorizer establishes owners' authorizations at declared OAuth Providers.
// The PKCE verifier travels sealed in the state, so nothing is kept between
// Begin and Complete and only this deployment can read it back.
type Authorizer struct {
	clients map[domain.Provider]Client
	cipher  application.CredentialCipher
	http    *http.Client
	now     func() time.Time
}

var _ application.OAuthProviderPort = (*Authorizer)(nil)

func NewAuthorizer(clients []Client, cipher application.CredentialCipher, httpClient *http.Client) (*Authorizer, error) {
	byProvider := make(map[domain.Provider]Client, len(clients))
	for _, client := range clients {
		if err := client.Validate(); err != nil {
			return nil, err
		}
		byProvider[client.Provider] = client
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Authorizer{clients: byProvider, cipher: cipher, http: noRedirects(httpClient), now: time.Now}, nil
}

type authorizationState struct {
	OwnerID     string `json:"o"`
	Provider    string `json:"p"`
	RedirectURI string `json:"r"`
	Verifier    string `json:"v"`
	IssuedAt    int64  `json:"t"`
}

func (a *Authorizer) BeginProviderAuthorization(_ context.Context, input application.BeginProviderAuthorizationInput) (application.BeginProviderAuthorizationResult, error) {
	client, ok := a.clients[input.Provider]
	if !ok {
		return application.BeginProviderAuthorizationResult{}, application.ErrProviderUnavailable
	}
	owner, redirect := strings.TrimSpace(input.OwnerID), strings.TrimSpace(input.RedirectURI)
	if owner == "" || redirect == "" {
		return application.BeginProviderAuthorizationResult{}, domain.ErrInvalidRequiredField
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return application.BeginProviderAuthorizationResult{}, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	payload, err := json.Marshal(authorizationState{OwnerID: owner, Provider: string(input.Provider), RedirectURI: redirect, Verifier: verifier, IssuedAt: a.now().Unix()})
	if err != nil {
		return application.BeginProviderAuthorizationResult{}, err
	}
	state, err := a.cipher.Encrypt(string(payload))
	if err != nil {
		return application.BeginProviderAuthorizationResult{}, err
	}
	challenge := sha256.Sum256([]byte(verifier))
	address, err := url.Parse(client.AuthorizationEndpoint)
	if err != nil {
		return application.BeginProviderAuthorizationResult{}, err
	}
	query := address.Query()
	query.Set("response_type", "code")
	query.Set("client_id", client.ClientID)
	query.Set("redirect_uri", redirect)
	query.Set("state", state)
	query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
	query.Set("code_challenge_method", "S256")
	if len(client.Scopes) > 0 {
		query.Set("scope", strings.Join(client.Scopes, " "))
	}
	for _, resource := range client.Resources {
		query.Add("resource", resource)
	}
	address.RawQuery = query.Encode()
	return application.BeginProviderAuthorizationResult{AuthorizationURL: address.String()}, nil
}

func (a *Authorizer) CompleteProviderAuthorization(ctx context.Context, input application.CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	client, ok := a.clients[input.Provider]
	if !ok {
		return nil, application.ErrProviderUnavailable
	}
	state, err := a.openState(input)
	if err != nil {
		return nil, err
	}
	code := strings.TrimSpace(input.Code)
	if code == "" {
		return nil, domain.ErrInvalidRequiredField
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {state.RedirectURI}, "code_verifier": {state.Verifier}}
	now := a.now().UTC()
	token, err := client.token(ctx, a.http, form)
	if err != nil {
		if grantRefused(err) {
			return nil, application.NewProviderError(application.ProviderErrorCredentialFailed, false, "the Provider refused the authorization code", err)
		}
		return nil, transient("the Provider did not issue credentials", err)
	}
	account, err := a.account(ctx, client, token)
	if err != nil {
		return nil, err
	}
	access, err := a.cipher.Encrypt(token.AccessToken)
	if err != nil {
		return nil, err
	}
	refresh := ""
	if strings.TrimSpace(token.RefreshToken) != "" {
		if refresh, err = a.cipher.Encrypt(token.RefreshToken); err != nil {
			return nil, err
		}
	}
	return domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerID: state.OwnerID, Provider: input.Provider, ProviderAccountID: account.ID, ProviderLogin: account.Login,
		AccessTokenCiphertext: access, RefreshTokenCiphertext: refresh,
		ExpiresAt: expiry(now, token.ExpiresIn), RefreshTokenExpiresAt: expiry(now, token.RefreshExpiresIn),
	})
}

// openState admits only a state this deployment sealed, unexpired, for the
// same owner, Provider and redirect.
func (a *Authorizer) openState(input application.CompleteProviderAuthorizationInput) (authorizationState, error) {
	plaintext, err := a.cipher.Decrypt(strings.TrimSpace(input.State))
	if err != nil {
		return authorizationState{}, domain.ErrPermissionDenied
	}
	var state authorizationState
	if json.Unmarshal([]byte(plaintext), &state) != nil {
		return authorizationState{}, domain.ErrPermissionDenied
	}
	issued := time.Unix(state.IssuedAt, 0)
	redirect := strings.TrimSpace(input.RedirectURI)
	if state.OwnerID != strings.TrimSpace(input.OwnerID) || state.Provider != string(input.Provider) || a.now().Sub(issued) > stateLifetime ||
		(redirect != "" && redirect != state.RedirectURI) {
		return authorizationState{}, domain.ErrPermissionDenied
	}
	return state, nil
}

type externalAccount struct{ ID, Login string }

// account names the authorized account from the ID Token the token endpoint
// returned over TLS, or else from the userinfo endpoint.
func (a *Authorizer) account(ctx context.Context, client Client, token tokenResponse) (externalAccount, error) {
	var claims struct {
		Subject           string `json:"sub"`
		PreferredUsername string `json:"preferred_username"`
		Email             string `json:"email"`
		Name              string `json:"name"`
	}
	switch {
	case token.IDToken != "":
		parts := strings.Split(token.IDToken, ".")
		payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(partOr(parts, 1), "="))
		if err != nil || json.Unmarshal(payload, &claims) != nil {
			return externalAccount{}, application.NewProviderError(application.ProviderErrorCredentialFailed, false, "the Provider's ID Token is unreadable", err)
		}
	case client.UserinfoEndpoint != "":
		if err := a.userinfo(ctx, client, token.AccessToken, &claims); err != nil {
			return externalAccount{}, err
		}
	default:
		return externalAccount{}, application.NewProviderError(application.ProviderErrorCredentialFailed, false, "the Provider names no account: request the openid scope or declare a userinfo endpoint", nil)
	}
	login := firstNonEmpty(claims.PreferredUsername, claims.Email, claims.Name, claims.Subject)
	if strings.TrimSpace(claims.Subject) == "" {
		return externalAccount{}, application.NewProviderError(application.ProviderErrorCredentialFailed, false, "the Provider names no account subject", nil)
	}
	return externalAccount{ID: claims.Subject, Login: login}, nil
}

func (a *Authorizer) userinfo(ctx context.Context, client Client, accessToken string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.UserinfoEndpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Accept", "application/json")
	response, err := a.http.Do(request)
	if err != nil {
		return transient("userinfo unavailable", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return transient("userinfo unavailable", fmt.Errorf("userinfo HTTP %d", response.StatusCode))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(out)
}

func partOr(parts []string, index int) string {
	if index < len(parts) {
		return parts[index]
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

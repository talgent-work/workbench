package github

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthtoken"
)

const providerAuthorizationStateTTL = 15 * time.Minute

type Authorizer struct {
	enabled bool
	cfg     Config
	app     *appClient
	cipher  TokenCipher
	now     func() time.Time
}

func NewAuthorizer(cfg Config, cipher TokenCipher) (*Authorizer, error) {
	app, err := newAppClient(cfg)
	if err != nil {
		return nil, err
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return nil, err
	}
	if enabled && cipher == nil {
		return nil, fmt.Errorf("github authorizer requires token cipher")
	}
	return &Authorizer{enabled: enabled, cfg: cfg, app: app, cipher: cipher, now: time.Now}, nil
}

// BeginProviderAuthorization always requests user authorization. The accounts
// and repositories it reaches are decided by the App's installations, which the
// user manages on GitHub; connecting needs no installation choice.
func (a *Authorizer) BeginProviderAuthorization(_ context.Context, input application.BeginProviderAuthorizationInput) (application.BeginProviderAuthorizationResult, error) {
	if !a.enabled || input.Provider != domain.ProviderGitHub {
		return application.BeginProviderAuthorizationResult{}, application.ErrProviderUnavailable
	}
	state, err := a.signState(providerAuthorizationState{
		OwnerID:      strings.TrimSpace(input.OwnerID),
		RedirectURI:  strings.TrimSpace(input.RedirectURI),
		IssuedAtUnix: a.now().UTC().Unix(),
	})
	if err != nil {
		return application.BeginProviderAuthorizationResult{}, err
	}
	return application.BeginProviderAuthorizationResult{AuthorizationURL: a.oauthAuthorizationURL(state, input.RedirectURI)}, nil
}

func (a *Authorizer) CompleteProviderAuthorization(ctx context.Context, input application.CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	if !a.enabled || input.Provider != domain.ProviderGitHub {
		return nil, application.ErrProviderUnavailable
	}
	state, err := a.verifyState(strings.TrimSpace(input.State))
	if err != nil {
		return nil, err
	}
	if state.OwnerID != strings.TrimSpace(input.OwnerID) {
		return nil, domain.ErrPermissionDenied
	}
	redirectURI := strings.TrimSpace(input.RedirectURI)
	if redirectURI == "" {
		redirectURI = state.RedirectURI
	}
	if state.RedirectURI != "" && redirectURI != state.RedirectURI {
		return nil, domain.ErrPermissionDenied
	}
	token, err := a.exchangeCode(ctx, strings.TrimSpace(input.Code), redirectURI)
	if err != nil {
		return nil, mapProviderError(err)
	}
	user, err := a.currentUser(ctx, token.AccessToken)
	if err != nil {
		return nil, mapProviderError(err)
	}
	accessCiphertext, err := a.cipher.Encrypt(token.AccessToken)
	if err != nil {
		return nil, err
	}
	refreshCiphertext := ""
	if strings.TrimSpace(token.RefreshToken) != "" {
		refreshCiphertext, err = a.cipher.Encrypt(token.RefreshToken)
		if err != nil {
			return nil, err
		}
	}
	expiresAt := time.Time{}
	if token.ExpiresIn > 0 {
		expiresAt = a.now().UTC().Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	refreshExpiresAt := time.Time{}
	if token.RefreshTokenExpiresIn > 0 {
		refreshExpiresAt = a.now().UTC().Add(time.Duration(token.RefreshTokenExpiresIn) * time.Second)
	}
	return domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerID:                strings.TrimSpace(input.OwnerID),
		Provider:               domain.ProviderGitHub,
		ProviderAccountID:      strconv.FormatInt(user.ID, 10),
		ProviderLogin:          user.Login,
		AccessTokenCiphertext:  accessCiphertext,
		RefreshTokenCiphertext: refreshCiphertext,
		ExpiresAt:              expiresAt,
		RefreshTokenExpiresAt:  refreshExpiresAt,
	})
}

func (a *Authorizer) oauthAuthorizationURL(state, redirectURI string) string {
	u, _ := url.Parse(a.cfg.authorizationEndpoint())
	q := u.Query()
	q.Set("client_id", strings.TrimSpace(a.cfg.OAuthClientID))
	q.Set("state", state)
	if strings.TrimSpace(redirectURI) != "" {
		q.Set("redirect_uri", strings.TrimSpace(redirectURI))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (a *Authorizer) exchangeCode(ctx context.Context, code, redirectURI string) (oauthtoken.Token, error) {
	if code == "" {
		return oauthtoken.Token{}, fmt.Errorf("github oauth code is required")
	}
	form := url.Values{"code": {code}}
	if strings.TrimSpace(redirectURI) != "" {
		form.Set("redirect_uri", strings.TrimSpace(redirectURI))
	}
	return a.cfg.tokenClient().Request(ctx, a.app.http, form)
}

func (a *Authorizer) currentUser(ctx context.Context, accessToken string) (githubUserResponse, error) {
	var resp githubUserResponse
	if err := a.app.doJSON(ctx, http.MethodGet, "/user", "Bearer "+accessToken, nil, &resp); err != nil {
		return githubUserResponse{}, err
	}
	if resp.ID == 0 || strings.TrimSpace(resp.Login) == "" {
		return githubUserResponse{}, fmt.Errorf("github user response is incomplete")
	}
	return resp, nil
}

func (a *Authorizer) signState(state providerAuthorizationState) (string, error) {
	if strings.TrimSpace(state.OwnerID) == "" {
		return "", domain.ErrInvalidRequiredField
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := a.sign(encoded)
	return encoded + "." + signature, nil
}

func (a *Authorizer) verifyState(raw string) (providerAuthorizationState, error) {
	var zero providerAuthorizationState
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return zero, domain.ErrPermissionDenied
	}
	expected := a.sign(parts[0])
	if subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) != 1 {
		return zero, domain.ErrPermissionDenied
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, domain.ErrPermissionDenied
	}
	var state providerAuthorizationState
	if err := json.Unmarshal(payload, &state); err != nil {
		return zero, domain.ErrPermissionDenied
	}
	if strings.TrimSpace(state.OwnerID) == "" {
		return zero, domain.ErrPermissionDenied
	}
	issuedAt := time.Unix(state.IssuedAtUnix, 0).UTC()
	if issuedAt.IsZero() || a.now().UTC().Sub(issuedAt) > providerAuthorizationStateTTL {
		return zero, domain.ErrPermissionDenied
	}
	return state, nil
}

func (a *Authorizer) sign(payload string) string {
	mac := hmac.New(sha256.New, credentialcipher.DeriveKey(a.cfg.TokenCipherKey))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

type providerAuthorizationState struct {
	OwnerID      string `json:"owner_id"`
	RedirectURI  string `json:"redirect_uri,omitempty"`
	IssuedAtUnix int64  `json:"issued_at_unix"`
}

type githubUserResponse struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

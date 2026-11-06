package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

const (
	defaultFreshTokenRequestSkew = 5 * time.Minute
	defaultRefreshLeaseDuration  = 30 * time.Second

	providerErrorGitHubReconnectRequired    application.ProviderErrorCode = "github_reconnect_required"
	providerErrorGitHubAuthorizationRevoked application.ProviderErrorCode = "github_authorization_revoked"
	providerErrorGitHubProviderTransient    application.ProviderErrorCode = "github_provider_transient"
	providerErrorGitHubRefreshInProgress    application.ProviderErrorCode = "github_refresh_in_progress"
	providerErrorGitHubTokenRefreshFailed   application.ProviderErrorCode = "github_token_refresh_failed"
)

type EnsureFreshUserTokenInput struct {
	OwnerUserID     string
	Provider        domain.Provider
	AuthorizationID string
	ForceRefresh    bool
	Reason          string
}

type FreshUserTokenManager struct {
	cfg            Config
	http           *http.Client
	authorizations application.AuthorizationStore
	cipher         TokenCipher
	now            func() time.Time
	requestSkew    time.Duration
	leaseDuration  time.Duration
}

type freshUserTokenManagerOption func(*FreshUserTokenManager)

func withFreshTokenRequestSkew(skew time.Duration) freshUserTokenManagerOption {
	return func(m *FreshUserTokenManager) {
		if skew > 0 {
			m.requestSkew = skew
		}
	}
}

func withFreshTokenRefreshLeaseDuration(duration time.Duration) freshUserTokenManagerOption {
	return func(m *FreshUserTokenManager) {
		if duration > 0 {
			m.leaseDuration = duration
		}
	}
}

func newFreshUserTokenManager(cfg Config, httpClient *http.Client, authorizations application.AuthorizationStore, cipher TokenCipher, now func() time.Time, opts ...freshUserTokenManagerOption) *FreshUserTokenManager {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	manager := &FreshUserTokenManager{
		cfg:            cfg,
		http:           httpClient,
		authorizations: authorizations,
		cipher:         cipher,
		now:            now,
		requestSkew:    defaultFreshTokenRequestSkew,
		leaseDuration:  defaultRefreshLeaseDuration,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	return manager
}

func (m *FreshUserTokenManager) EnsureFreshUserToken(ctx context.Context, input EnsureFreshUserTokenInput) (*domain.ProviderAuthorization, string, error) {
	if strings.TrimSpace(input.OwnerUserID) == "" {
		return nil, "", domain.ErrPermissionDenied
	}
	auth, err := m.EnsureFreshAuthorization(ctx, input)
	if err != nil {
		return nil, "", err
	}
	// This adapter calls the configured GitHub API for its verified built-in
	// operations. Arbitrary consumer targets are verified before reaching it.
	credentials, err := auth.SupplyValidCredentials(ctx, domain.CredentialRequirement{OwnerUserID: input.OwnerUserID, Provider: domain.ProviderGitHub, Applicable: true}, m.now().UTC(), 0, m.authorizationPort())
	if err != nil {
		return nil, "", authorizationError(err)
	}
	token, err := m.cipher.Decrypt(credentials.AccessTokenCiphertext)
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, "", application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "unable to decrypt access credential", err)
	}
	return auth, token, nil
}

func (m *FreshUserTokenManager) authorizationPort() *AuthorizationPort {
	return &AuthorizationPort{cfg: m.cfg, http: m.http, cipher: m.cipher, now: m.now}
}

func (m *FreshUserTokenManager) EnsureFreshAuthorization(ctx context.Context, input EnsureFreshUserTokenInput) (*domain.ProviderAuthorization, error) {
	auth, err := m.loadAuthorization(ctx, input)
	if err != nil {
		return nil, err
	}
	if err := rejectUnusableAuthorization(auth); err != nil {
		return nil, err
	}
	now := m.now().UTC()
	if !input.ForceRefresh && auth.CanUseToken(now, m.requestSkew) {
		return auth, nil
	}
	refreshed, _, err := m.refreshUserToken(ctx, auth, now)
	return refreshed, err
}

func (m *FreshUserTokenManager) loadAuthorization(ctx context.Context, input EnsureFreshUserTokenInput) (*domain.ProviderAuthorization, error) {
	if input.Provider != "" && input.Provider != domain.ProviderGitHub {
		return nil, application.ErrProviderUnavailable
	}
	if strings.TrimSpace(input.AuthorizationID) != "" {
		auth, err := m.authorizations.Get(ctx, strings.TrimSpace(input.AuthorizationID))
		if err != nil {
			return nil, providerUnavailableForNotFound(err)
		}
		if auth.Provider != domain.ProviderGitHub || (input.OwnerUserID != "" && auth.OwnerUserID != input.OwnerUserID) {
			return nil, domain.ErrPermissionDenied
		}
		return auth, nil
	}
	auth, err := m.authorizations.GetByOwnerAndProvider(ctx, strings.TrimSpace(input.OwnerUserID), domain.ProviderGitHub)
	if err != nil {
		return nil, providerUnavailableForNotFound(err)
	}
	if auth.OwnerUserID != input.OwnerUserID || auth.Provider != domain.ProviderGitHub {
		return nil, domain.ErrPermissionDenied
	}
	return auth, nil
}

func (m *FreshUserTokenManager) refreshUserToken(ctx context.Context, auth *domain.ProviderAuthorization, now time.Time) (*domain.ProviderAuthorization, string, error) {
	leaseID := uuid.NewString()
	leased, err := m.authorizations.AcquireCredentialLease(ctx, auth, leaseID, now.Add(m.leaseDuration), now)
	if err != nil {
		if errors.Is(err, domain.ErrConcurrentModification) {
			return m.reloadAfterConcurrentRefresh(ctx, auth)
		}
		return nil, "", application.NewProviderError(providerErrorGitHubProviderTransient, true, "acquire refresh lease", err)
	}
	if err := leased.RenewCredentials(ctx, now, m.requestSkew, true, m.authorizationPort()); err != nil {
		if errors.Is(err, domain.ErrAuthorizationReconnectRequired) {
			if saveErr := m.authorizations.Save(ctx, leased); saveErr != nil {
				return nil, "", saveErr
			}
			return nil, "", authorizationError(err)
		}
		return nil, "", m.recordTransientFailure(ctx, leased, "GitHub credential renewal unavailable", err, now)
	}
	if err := m.authorizations.Save(ctx, leased); err != nil {
		return nil, "", application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "persist refreshed credential", err)
	}
	token, err := m.cipher.Decrypt(leased.AccessTokenCiphertext)
	if err != nil {
		return nil, "", application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "decrypt refreshed credential", err)
	}
	return leased, token, nil
}

func (m *FreshUserTokenManager) reloadAfterConcurrentRefresh(ctx context.Context, stale *domain.ProviderAuthorization) (*domain.ProviderAuthorization, string, error) {
	reloaded, err := m.authorizations.Get(ctx, stale.ID)
	if err != nil {
		return nil, "", application.NewProviderError(providerErrorGitHubRefreshInProgress, true, "refresh in progress", err)
	}
	now := m.now().UTC()
	if err := rejectUnusableAuthorization(reloaded); err != nil {
		return nil, "", err
	}
	if reloaded.CanUseToken(now, m.requestSkew) {
		token, err := m.cipher.Decrypt(reloaded.AccessTokenCiphertext)
		if err != nil {
			return nil, "", application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "decrypt refreshed access token", err)
		}
		if strings.TrimSpace(token) != "" {
			return reloaded, token, nil
		}
	}
	return nil, "", application.NewProviderError(providerErrorGitHubRefreshInProgress, true, "refresh in progress", domain.ErrConcurrentModification)
}

func (m *FreshUserTokenManager) recordTransientFailure(ctx context.Context, auth *domain.ProviderAuthorization, detail string, cause error, now time.Time) error {
	if err := auth.RecordTransientFailure(string(providerErrorGitHubProviderTransient), detail, now); err != nil {
		return application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "record transient failure", err)
	}
	if err := m.authorizations.Save(ctx, auth); err != nil {
		return application.NewProviderError(providerErrorGitHubTokenRefreshFailed, false, "persist transient failure", err)
	}
	return application.NewProviderError(providerErrorGitHubProviderTransient, true, detail, cause)
}

func rejectUnusableAuthorization(auth *domain.ProviderAuthorization) error {
	if auth == nil {
		return authorizationError(domain.ErrAuthorizationReconnectRequired)
	}
	return authorizationError(auth.CredentialSupplyError())
}

func authorizationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrAuthorizationReconnectRequired):
		return application.NewProviderError(providerErrorGitHubReconnectRequired, false, "authorization requires reconnection", err)
	case errors.Is(err, domain.ErrAuthorizationRevoked):
		return application.NewProviderError(providerErrorGitHubAuthorizationRevoked, false, "authorization revoked", err)
	default:
		return err
	}
}

func providerUnavailableForNotFound(err error) error {
	if errors.Is(err, domain.ErrProviderNotFound) {
		return application.NewProviderError(providerErrorGitHubReconnectRequired, false, "authorization missing", application.ErrProviderUnavailable)
	}
	return application.NewProviderError(providerErrorGitHubProviderTransient, true, "load authorization", err)
}

func refreshOAuthToken(ctx context.Context, cfg Config, httpClient *http.Client, refreshToken string) (oauthTokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return oauthTokenResponse{}, application.NewProviderError(providerErrorGitHubReconnectRequired, false, "refresh token unavailable", application.ErrProviderUnavailable)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(cfg.OAuthClientID))
	form.Set("client_secret", strings.TrimSpace(cfg.OAuthClientSecret))
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.webBaseURL()+"/login/oauth/access_token", bytes.NewBufferString(form.Encode()))
	if err != nil {
		return oauthTokenResponse{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return oauthTokenResponse{}, err
	}
	defer resp.Body.Close()
	var out oauthTokenResponse
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = json.Unmarshal(body, &out)
		if strings.TrimSpace(out.Error) != "" {
			return oauthTokenResponse{}, &githubOAuthError{StatusCode: resp.StatusCode, Code: out.Error, Description: out.ErrorDescription}
		}
		return oauthTokenResponse{}, fmt.Errorf("github oauth refresh status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return oauthTokenResponse{}, err
	}
	if strings.TrimSpace(out.Error) != "" {
		return oauthTokenResponse{}, &githubOAuthError{Code: out.Error, Description: out.ErrorDescription}
	}
	if strings.TrimSpace(out.AccessToken) == "" || strings.TrimSpace(out.RefreshToken) == "" || out.ExpiresIn <= 0 || out.RefreshTokenExpiresIn <= 0 {
		return oauthTokenResponse{}, &githubOAuthError{Code: "incomplete_response"}
	}
	return out, nil
}

type githubOAuthError struct {
	StatusCode  int
	Code        string
	Description string
}

func (e *githubOAuthError) Error() string {
	if e == nil {
		return ""
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("github oauth error %s status %d", e.Code, e.StatusCode)
	}
	return "github oauth error " + e.Code
}

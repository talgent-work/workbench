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

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type AuthorizationPort struct {
	cfg    Config
	http   *http.Client
	cipher TokenCipher
	now    func() time.Time
}

var _ domain.ProviderAuthorizationPort = (*AuthorizationPort)(nil)

func NewAuthorizationPort(cfg Config, cipher TokenCipher) (*AuthorizationPort, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &AuthorizationPort{cfg: cfg, http: http.DefaultClient, cipher: cipher, now: time.Now}, nil
}

func (p *AuthorizationPort) Renew(ctx context.Context, credentials domain.CredentialSet) (domain.CredentialRenewal, error) {
	if enabled, err := p.cfg.enabled(); err != nil || !enabled {
		return domain.CredentialRenewal{}, application.ErrProviderUnavailable
	}
	refresh, err := p.cipher.Decrypt(credentials.RefreshTokenCiphertext)
	if err != nil {
		return domain.CredentialRenewal{}, application.NewProviderError(providerErrorGitHubProviderTransient, true, "decrypt refresh credential", err)
	}
	if strings.TrimSpace(refresh) == "" {
		return reconnectResult(), nil
	}
	client := *p.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, err := refreshOAuthToken(ctx, p.cfg, &client, refresh)
	if err != nil {
		var oauthErr *githubOAuthError
		// GitHub reports an unusable refresh token as bad_refresh_token,
		// including in HTTP 200 responses; retrying cannot restore that grant.
		if errors.As(err, &oauthErr) && (oauthErr.Code == "bad_refresh_token" || oauthErr.Code == "invalid_grant" || oauthErr.Code == "incomplete_response") {
			return reconnectResult(), nil
		}
		return domain.CredentialRenewal{}, application.NewProviderError(providerErrorGitHubProviderTransient, true, "GitHub credential renewal unavailable", err)
	}
	accessCiphertext, err := p.cipher.Encrypt(token.AccessToken)
	if err != nil {
		return reconnectResult(), nil
	}
	refreshCiphertext, err := p.cipher.Encrypt(token.RefreshToken)
	if err != nil {
		return reconnectResult(), nil
	}
	now := p.now().UTC()
	return domain.CredentialRenewal{Credentials: domain.CredentialSet{
		AccessTokenCiphertext: accessCiphertext, RefreshTokenCiphertext: refreshCiphertext,
		ExpiresAt: now.Add(time.Duration(token.ExpiresIn) * time.Second), RefreshTokenExpiresAt: now.Add(time.Duration(token.RefreshTokenExpiresIn) * time.Second),
	}}, nil
}

func reconnectResult() domain.CredentialRenewal {
	return domain.CredentialRenewal{ReconnectRequired: true, ReasonCode: string(providerErrorGitHubReconnectRequired)}
}

// Revoke uses GitHub's app authorization endpoint, which revokes the user's
// grant and its associated tokens. Only a 204 confirms completion; an invalid
// token response is not evidence that the whole authorization was revoked.
func (p *AuthorizationPort) Revoke(ctx context.Context, credentials domain.CredentialSet) (bool, error) {
	if enabled, err := p.cfg.enabled(); err != nil || !enabled {
		return false, application.ErrProviderUnavailable
	}
	access, err := p.cipher.Decrypt(credentials.AccessTokenCiphertext)
	if err != nil {
		return false, application.NewProviderError("github_revocation_unconfirmed", false, "unable to obtain revocation credential", err)
	}
	if strings.TrimSpace(access) == "" {
		return false, domain.ErrRevocationUnconfirmed
	}
	body, err := json.Marshal(struct {
		AccessToken string `json:"access_token"`
	}{access})
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		p.cfg.apiBaseURL()+"/applications/"+url.PathEscape(p.cfg.OAuthClientID)+"/grant", bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.SetBasicAuth(p.cfg.OAuthClientID, p.cfg.OAuthClientSecret)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-GitHub-Api-Version", p.cfg.apiVersion())
	client := *p.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return false, application.NewProviderError("github_revocation_unconfirmed", true, "GitHub revocation unavailable", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusNoContent {
		return false, application.NewProviderError("github_revocation_unconfirmed", response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests,
			fmt.Sprintf("GitHub did not confirm revocation (HTTP %d)", response.StatusCode), domain.ErrRevocationUnconfirmed)
	}
	return true, nil
}

package oauthprovider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthtoken"
)

// Port is one OAuth Provider's adapter for the ProviderAuthorizationPort.
type Port struct {
	client Client
	cipher application.CredentialCipher
	http   *http.Client
	now    func() time.Time
}

var _ domain.ProviderAuthorizationPort = (*Port)(nil)

func NewPort(client Client, cipher application.CredentialCipher, httpClient *http.Client) (*Port, error) {
	if err := client.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Port{client: client, cipher: cipher, http: noRedirects(httpClient), now: time.Now}, nil
}

// Renew exchanges the refresh credential for a new access credential. A
// server that rotates refresh credentials returns the replacement; one that
// does not keeps the current one valid.
func (p *Port) Renew(ctx context.Context, credentials domain.CredentialSet) (domain.CredentialRenewal, error) {
	token, refresh, err := p.refresh(ctx, credentials, "")
	if grantRefused(err) {
		return domain.CredentialRenewal{ReconnectRequired: true, ReasonCode: "provider_grant_refused"}, nil
	}
	if err != nil {
		return domain.CredentialRenewal{}, err
	}
	access, err := p.cipher.Encrypt(token.AccessToken)
	if err != nil {
		return domain.CredentialRenewal{}, err
	}
	now := p.now().UTC()
	renewed := domain.CredentialSet{AccessTokenCiphertext: access, RefreshTokenCiphertext: credentials.RefreshTokenCiphertext,
		ExpiresAt: expiry(now, token.ExpiresIn), RefreshTokenExpiresAt: credentials.RefreshTokenExpiresAt}
	if refresh != "" {
		renewed.RefreshTokenCiphertext, renewed.RefreshTokenExpiresAt = refresh, expiry(now, token.RefreshLifetime())
	}
	return domain.CredentialRenewal{Credentials: renewed}, nil
}

// ObtainTargetCredential asks for an access credential bound to the target's
// resource indicator with the refresh credential (RFC 8707, section 2.2).
func (p *Port) ObtainTargetCredential(ctx context.Context, credentials domain.CredentialSet, target domain.ProviderTarget) (domain.TargetCredentialOutcome, error) {
	resource := p.client.TargetResources[target.Name]
	if resource == "" {
		return domain.TargetCredentialOutcome{}, domain.ErrCredentialTargetInapplicable
	}
	token, refresh, err := p.refresh(ctx, credentials, resource)
	if grantRefused(err) {
		return domain.TargetCredentialOutcome{ReconnectRequired: true, ReasonCode: "provider_grant_refused"}, nil
	}
	if err != nil {
		return domain.TargetCredentialOutcome{}, err
	}
	access, err := p.cipher.Encrypt(token.AccessToken)
	if err != nil {
		return domain.TargetCredentialOutcome{}, err
	}
	now := p.now().UTC()
	outcome := domain.TargetCredentialOutcome{Credential: domain.TargetCredential{TokenCiphertext: access, ExpiresAt: expiry(now, token.ExpiresIn)}}
	if refresh != "" {
		outcome.RefreshTokenCiphertext, outcome.RefreshTokenExpiresAt = refresh, expiry(now, token.RefreshLifetime())
	}
	return outcome, nil
}

// refresh runs the refresh grant, for one resource when named, and returns
// the protected replacement refresh credential when the server rotated it.
func (p *Port) refresh(ctx context.Context, credentials domain.CredentialSet, resource string) (oauthtoken.Token, string, error) {
	refreshToken, err := p.cipher.Decrypt(credentials.RefreshTokenCiphertext)
	if err != nil || strings.TrimSpace(refreshToken) == "" {
		return oauthtoken.Token{}, "", &oauthtoken.Error{Code: "invalid_grant"}
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
	if resource != "" {
		form.Set("resource", resource)
	}
	token, err := p.client.endpoint().Request(ctx, p.http, form)
	if err != nil {
		if grantRefused(err) {
			return oauthtoken.Token{}, "", err
		}
		return oauthtoken.Token{}, "", transient(fmt.Sprintf("Provider %s credential renewal unavailable", p.client.Provider), err)
	}
	if token.RefreshToken == "" || token.RefreshToken == refreshToken {
		return token, "", nil
	}
	rotated, err := p.cipher.Encrypt(token.RefreshToken)
	if err != nil {
		return oauthtoken.Token{}, "", err
	}
	return token, rotated, nil
}

// Revoke revokes the refresh credential, and with it the grant, at the
// declared revocation endpoint (RFC 7009); only a 200 confirms it.
func (p *Port) Revoke(ctx context.Context, credentials domain.CredentialSet) (bool, error) {
	if p.client.RevocationEndpoint == "" {
		return false, application.NewProviderError("revocation_unsupported", false, "the Provider declares no revocation endpoint", domain.ErrRevocationUnconfirmed)
	}
	ciphertext, hint := credentials.RefreshTokenCiphertext, "refresh_token"
	if strings.TrimSpace(ciphertext) == "" {
		ciphertext, hint = credentials.AccessTokenCiphertext, "access_token"
	}
	token, err := p.cipher.Decrypt(ciphertext)
	if err != nil || strings.TrimSpace(token) == "" {
		return false, domain.ErrRevocationUnconfirmed
	}
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	request, err := p.client.endpoint().Post(ctx, p.client.RevocationEndpoint, url.Values{"token": {token}, "token_type_hint": {hint}})
	if err != nil {
		return false, err
	}
	response, err := p.http.Do(request)
	if err != nil {
		return false, application.NewProviderError("revocation_unconfirmed", true, "Provider revocation unavailable", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		return false, application.NewProviderError("revocation_unconfirmed", response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests,
			fmt.Sprintf("the Provider did not confirm revocation (HTTP %d)", response.StatusCode), domain.ErrRevocationUnconfirmed)
	}
	return true, nil
}

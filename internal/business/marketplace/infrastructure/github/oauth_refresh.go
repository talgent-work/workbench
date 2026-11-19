package github

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthtoken"
)

const (
	providerErrorGitHubReconnectRequired  application.ProviderErrorCode = "github_reconnect_required"
	providerErrorGitHubProviderTransient  application.ProviderErrorCode = "github_provider_transient"
	providerErrorGitHubTokenRefreshFailed application.ProviderErrorCode = "github_token_refresh_failed"
)

// refreshOAuthToken renews at GitHub's token endpoint. GitHub answers a
// refused refresh token as bad_refresh_token, even with HTTP 200; a renewal
// GitHub completes always rotates both credentials and states both expiries.
func refreshOAuthToken(ctx context.Context, cfg Config, httpClient *http.Client, refreshToken string) (oauthtoken.Token, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return oauthtoken.Token{}, application.NewProviderError(providerErrorGitHubReconnectRequired, false, "refresh token unavailable", application.ErrProviderUnavailable)
	}
	token, err := cfg.tokenClient().Request(ctx, httpClient, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if err != nil {
		return oauthtoken.Token{}, err
	}
	if strings.TrimSpace(token.RefreshToken) == "" || token.ExpiresIn <= 0 || token.RefreshTokenExpiresIn <= 0 {
		return oauthtoken.Token{}, &oauthtoken.Error{Code: "incomplete_response"}
	}
	return token, nil
}

// refreshRefused reports a refresh GitHub will never complete.
func refreshRefused(err error) bool {
	return oauthtoken.Refused(err, "bad_refresh_token", "invalid_grant", "incomplete_response")
}

package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
)

const (
	providerErrorGitHubReconnectRequired  application.ProviderErrorCode = "github_reconnect_required"
	providerErrorGitHubProviderTransient  application.ProviderErrorCode = "github_provider_transient"
	providerErrorGitHubTokenRefreshFailed application.ProviderErrorCode = "github_token_refresh_failed"
)

// refreshTimeout bounds one renewal at GitHub.
const refreshTimeout = 15 * time.Second

func refreshOAuthToken(ctx context.Context, cfg Config, httpClient *http.Client, refreshToken string) (oauthTokenResponse, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return oauthTokenResponse{}, application.NewProviderError(providerErrorGitHubReconnectRequired, false, "refresh token unavailable", application.ErrProviderUnavailable)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	form := url.Values{}
	form.Set("client_id", strings.TrimSpace(cfg.OAuthClientID))
	form.Set("client_secret", strings.TrimSpace(cfg.OAuthClientSecret))
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.tokenEndpoint(), bytes.NewBufferString(form.Encode()))
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

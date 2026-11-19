// Package oauthprovider reaches the OAuth authorization server of every
// declared Provider apart from GitHub: it authorizes owners through the
// authorization code flow with PKCE, renews their credentials, obtains a
// credential per target by resource indicator (RFC 8707) and revokes
// authorizations (RFC 7009).
package oauthprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// providerTimeout bounds one call to an authorization server.
const providerTimeout = 15 * time.Second

// Client is one Provider's OAuth client at its authorization server.
type Client struct {
	Provider              domain.Provider
	ClientID              string
	ClientSecret          string
	AuthorizationEndpoint string
	TokenEndpoint         string
	// RevocationEndpoint is optional; without it no authorization can be
	// confirmed revoked.
	RevocationEndpoint string
	// UserinfoEndpoint is optional; the ID Token names the account otherwise.
	UserinfoEndpoint string
	Scopes           []string
	// Resources are the declared targets' resource indicators, requested with
	// the authorization so each target's credential lies within the grant
	// (RFC 8707, section 2).
	Resources []string
}

func (c Client) Validate() error {
	if strings.TrimSpace(c.ClientID) == "" {
		return fmt.Errorf("Provider %s: client-id is required", c.Provider)
	}
	for name, endpoint := range map[string]string{"authorization-endpoint": c.AuthorizationEndpoint, "token-endpoint": c.TokenEndpoint,
		"revocation-endpoint": c.RevocationEndpoint, "userinfo-endpoint": c.UserinfoEndpoint} {
		if endpoint == "" && (name == "revocation-endpoint" || name == "userinfo-endpoint") {
			continue
		}
		u, err := url.Parse(endpoint)
		if err != nil || u.Host == "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && loopback(u.Hostname()))) {
			return fmt.Errorf("Provider %s: %s must be an absolute HTTPS address", c.Provider, name)
		}
	}
	return nil
}

// loopback admits plain HTTP only to this machine, where a client secret and
// tokens never cross a network.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// tokenResponse is an OAuth token endpoint response; refresh_expires_in is
// the refresh token's lifetime where the server reports one.
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int64  `json:"expires_in"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	IDToken          string `json:"id_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// oauthError is an error the authorization server answered.
type oauthError struct {
	StatusCode int
	Code       string
}

func (e *oauthError) Error() string {
	return fmt.Sprintf("oauth error %s (HTTP %d)", e.Code, e.StatusCode)
}

// grantRefused reports a refresh credential the server will never accept
// again; retrying cannot restore it.
func grantRefused(err error) bool {
	var refused *oauthError
	return errors.As(err, &refused) && (refused.Code == "invalid_grant" || refused.Code == "unauthorized_client")
}

// token posts a grant to the token endpoint with the client's credentials.
func (c Client) token(ctx context.Context, httpClient *http.Client, form url.Values) (tokenResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	request, err := c.post(ctx, c.TokenEndpoint, form)
	if err != nil {
		return tokenResponse{}, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return tokenResponse{}, err
	}
	defer response.Body.Close()
	var out tokenResponse
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, err
	}
	if json.Unmarshal(body, &out) != nil && response.StatusCode < 300 {
		return tokenResponse{}, fmt.Errorf("oauth token response is not JSON (HTTP %d)", response.StatusCode)
	}
	if out.Error != "" || response.StatusCode >= 300 {
		return tokenResponse{}, &oauthError{StatusCode: response.StatusCode, Code: out.Error}
	}
	if strings.TrimSpace(out.AccessToken) == "" {
		return tokenResponse{}, &oauthError{StatusCode: response.StatusCode, Code: "incomplete_response"}
	}
	return out, nil
}

// post authenticates the client with HTTP Basic when it has a secret, and
// names it in the form when it is public.
func (c Client) post(ctx context.Context, endpoint string, form url.Values) (*http.Request, error) {
	if c.ClientSecret == "" {
		form.Set("client_id", c.ClientID)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	if c.ClientSecret != "" {
		request.SetBasicAuth(url.QueryEscape(c.ClientID), url.QueryEscape(c.ClientSecret))
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	return request, nil
}

// noRedirects keeps every credential-bearing request at the address declared.
func noRedirects(client *http.Client) *http.Client {
	copied := *client
	copied.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &copied
}

func expiry(now time.Time, seconds int64) time.Time {
	if seconds <= 0 {
		return time.Time{}
	}
	return now.Add(time.Duration(seconds) * time.Second).UTC()
}

func transient(description string, err error) error {
	return application.NewProviderError(application.ProviderErrorTransient, true, description, err)
}

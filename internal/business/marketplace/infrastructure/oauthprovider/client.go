// Package oauthprovider reaches the OAuth authorization server of every
// declared Provider apart from GitHub: it authorizes owners through the
// authorization code flow with PKCE, renews their credentials, obtains a
// credential per target by resource indicator (RFC 8707) and revokes
// authorizations (RFC 7009).
package oauthprovider

import (
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthtoken"
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
	// TargetResources name each target of a per-target Provider as an OAuth
	// resource indicator (RFC 8707), by target name: its credential is
	// requested for that resource.
	TargetResources map[string]string
}

// resources are the target resources in a stable order.
func (c Client) resources() []string {
	names := slices.Sorted(maps.Keys(c.TargetResources))
	resources := make([]string, len(names))
	for i, name := range names {
		resources[i] = c.TargetResources[name]
	}
	return resources
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
	for target, resource := range c.TargetResources {
		if u, err := url.Parse(resource); err != nil || !u.IsAbs() || u.Fragment != "" {
			return fmt.Errorf("Provider %s: target %s needs an absolute resource without a fragment", c.Provider, target)
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

// grantRefused reports a refresh credential the server will never accept
// again; retrying cannot restore it.
func grantRefused(err error) bool {
	return oauthtoken.Refused(err, "invalid_grant", "unauthorized_client")
}

// endpoint is the client at its token endpoint.
func (c Client) endpoint() oauthtoken.Endpoint {
	return oauthtoken.Endpoint{URL: c.TokenEndpoint, ClientID: c.ClientID, ClientSecret: c.ClientSecret}
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

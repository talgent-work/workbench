//go:build integration

package infrastructure_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// The resource indicators (RFC 8707) by which scripts/test/oidc-provider
// knows the docs and the ticket services.
const (
	docsResource    = "https://docs.example.com"
	ticketsResource = "https://tickets.example.com"
)

// perTargetIdP declares an identity provider whose one authorization obtains
// a distinct credential for each of the docs and the ticket services.
func perTargetIdP() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example SSO", Derivation: domain.PerTargetCredential, CredentialsExpire: true,
		Targets: []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}, {Name: "tickets", Hosts: []domain.HostPattern{"*.tickets.example.com"}}}}
}

// newOIDCProviderFixture reaches the panva/node-oidc-provider server
// scripts/test/oidc-provider configures.
func newOIDCProviderFixture(t *testing.T) *oauthFixture {
	t.Helper()
	issuer := middlewaretest.Environment(t, "TALGENT_TEST_OIDC_PROVIDER_URL")
	client := oauthprovider.Client{Provider: "example-sso", ClientID: oauthClientID, ClientSecret: oauthSecret, Scopes: []string{"openid", "api"},
		AuthorizationEndpoint: issuer + "/auth", TokenEndpoint: issuer + "/token", RevocationEndpoint: issuer + "/token/revocation",
		TargetResources: map[string]string{"docs": docsResource, "tickets": ticketsResource}}
	return newOAuthFixture(t, client, perTargetIdP(), issuer+"/token/introspection")
}

// One authorization, established through the authorization code flow with
// PKCE for every target's resource, yields a distinct credential per target
// whose audience is that target alone; renewal keeps both valid.
func TestOneAuthorizationYieldsACredentialPerTargetByResource(t *testing.T) {
	f := newOIDCProviderFixture(t)
	ctx := t.Context()
	owner := "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c72"

	begun, err := f.public.BeginProviderAuthorization(as(ctx, owner), &marketplacepublicv1.BeginProviderAuthorizationRequest{Provider: "example-sso", RedirectUri: oauthRedirect})
	require.NoError(t, err)
	authorization, err := url.Parse(begun.AuthorizationUrl)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{docsResource, ticketsResource}, authorization.Query()["resource"], "one authorization covers every target's resource")
	code, state := signIn(t, begun.AuthorizationUrl, "alice", "fixture-only")
	completed, err := f.public.CompleteProviderAuthorization(as(ctx, owner), &marketplacepublicv1.CompleteProviderAuthorizationRequest{Provider: "example-sso", Code: code, State: state, RedirectUri: oauthRedirect})
	require.NoError(t, err)
	require.Equal(t, "active", completed.Authorization.Status)
	require.Equal(t, "alice", completed.Authorization.ProviderLogin)
	authorizations, err := f.public.ListPersonalAuthorizations(as(ctx, owner), &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	require.NoError(t, err)
	require.Len(t, authorizations.Authorizations, 1)
	id := authorizations.Authorizations[0].Id

	// Each service, as a resource server, accepts its own target's
	// credential, whose audience is that service alone, and not the other's.
	confined := func(docs, tickets *marketplaceprivatev1.SupplyServiceCredentialResponse) {
		t.Helper()
		require.NotEqual(t, docs.Token, tickets.Token, "each target has its own credential")
		for _, c := range []struct {
			token, service, resource, other string
		}{{docs.Token, "service-docs", docsResource, "service-tickets"}, {tickets.Token, "service-tickets", ticketsResource, "service-docs"}} {
			own := f.introspect(t, c.service, c.token)
			require.True(t, own.Active, "%s accepts its credential", c.service)
			require.Equal(t, c.resource, own.Audience, "the credential's audience is its target alone")
			require.False(t, f.accepts(t, c.other, c.token), "%s refuses a credential for another target", c.other)
		}
	}
	docs, tickets := f.supplyTarget(t, owner, id, "docs"), f.supplyTarget(t, owner, id, "tickets")
	require.Equal(t, []string{"docs.example.com"}, docs.Hosts)
	require.Equal(t, []string{"*.tickets.example.com"}, tickets.Hosts)
	require.NotZero(t, docs.ExpiresAtUnixMs, "the declared expiry is the credential's own")
	confined(docs, tickets)
	require.Equal(t, docs.Token, f.supplyTarget(t, owner, id, "docs").Token, "a current target credential is reused")

	require.NoError(t, f.credentials.RenewAuthorization(ctx, id), "the credential expires within the proactive window")
	renewedDocs, renewedTickets := f.supplyTarget(t, owner, id, "docs"), f.supplyTarget(t, owner, id, "tickets")
	require.NotEqual(t, docs.Token, renewedDocs.Token)
	require.NotEqual(t, tickets.Token, renewedTickets.Token)
	confined(renewedDocs, renewedTickets)
	require.True(t, strings.HasPrefix(f.refreshCiphertext(t, id), "v1:"), "the refresh credential stays encrypted at rest")
	var targets string
	_, err = f.db.SQL("SELECT target_credentials_json FROM scm_provider_authorization WHERE id = ?", id).Get(&targets)
	require.NoError(t, err)
	require.Contains(t, targets, "v1:", "both target credentials are kept")
	require.NotContains(t, targets, renewedDocs.Token, "target credentials stay encrypted at rest")
	require.NotContains(t, targets, renewedTickets.Token)
}

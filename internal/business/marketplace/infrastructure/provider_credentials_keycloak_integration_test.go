//go:build integration

package infrastructure_test

import (
	"net/url"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// enterpriseIdP declares the realm as an enterprise identity provider: one
// authorization serves the docs and the ticket services, whose credentials
// expire. It is shared, not per-target: Keycloak (26.8.2, its resource-indicators
// feature) binds an authorization code and its refresh token to at most one
// resource, refusing a repeated resource as a duplicated parameter on the
// authorization, PAR and token endpoints and any other resource on refresh
// (ResourceIndicatorsPostProcessor), so one authorization cannot yield a
// credential per resource there.
func enterpriseIdP() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example SSO", Derivation: domain.SharedTargetCredential, CredentialsExpire: true,
		Targets: []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}, {Name: "tickets", Hosts: []domain.HostPattern{"*.tickets.example.com"}, Paths: []domain.PathPattern{"/api/*"}, Methods: []string{"GET", "POST"}}}}
}

// newKeycloakFixture reaches the realm scripts/test/keycloak/realm.json
// declares: Workbench's client, whose tokens are meant for the docs and the
// ticket services.
func newKeycloakFixture(t *testing.T) *oauthFixture {
	t.Helper()
	issuer := middlewaretest.Environment(t, "TALGENT_TEST_KEYCLOAK_URL")
	client := oauthprovider.Client{Provider: "example-sso", ClientID: oauthClientID, ClientSecret: oauthSecret, Scopes: []string{"openid"},
		AuthorizationEndpoint: issuer + "/protocol/openid-connect/auth", TokenEndpoint: issuer + "/protocol/openid-connect/token",
		RevocationEndpoint: issuer + "/protocol/openid-connect/revoke"}
	return newOAuthFixture(t, client, enterpriseIdP(), issuer+"/protocol/openid-connect/token/introspect")
}

// One authorization at an enterprise identity provider, established through
// the authorization code flow with PKCE, serves both declared targets with a
// credential the provider accepts, confined to each target's hosts, and
// renewal keeps both valid.
func TestKeycloakAuthorizationServesEveryDeclaredTarget(t *testing.T) {
	f := newKeycloakFixture(t)
	ctx := t.Context()
	owner := "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c71"

	begun, err := f.public.BeginProviderAuthorization(as(ctx, owner), &marketplacepublicv1.BeginProviderAuthorizationRequest{Provider: "example-sso", RedirectUri: oauthRedirect})
	require.NoError(t, err)
	authorization, err := url.Parse(begun.AuthorizationUrl)
	require.NoError(t, err)
	require.Equal(t, "S256", authorization.Query().Get("code_challenge_method"))
	code, state := signIn(t, begun.AuthorizationUrl, "alice", "fixture-only")
	completed, err := f.public.CompleteProviderAuthorization(as(ctx, owner), &marketplacepublicv1.CompleteProviderAuthorizationRequest{Provider: "example-sso", Code: code, State: state, RedirectUri: oauthRedirect})
	require.NoError(t, err)
	require.Equal(t, "active", completed.Authorization.Status)
	require.Equal(t, "alice", completed.Authorization.ProviderLogin)

	listed, err := f.public.ListProviders(as(ctx, owner), &marketplacepublicv1.ListProvidersRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Providers, 1)
	require.Equal(t, "active", listed.Providers[0].Connection.Status)
	require.True(t, listed.Providers[0].TargetCredentialsExpire)
	require.False(t, listed.Providers[0].ObservesAccessScope)

	authorizations, err := f.public.ListPersonalAuthorizations(as(ctx, owner), &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	require.NoError(t, err)
	require.Len(t, authorizations.Authorizations, 1)
	require.ElementsMatch(t, []string{"docs", "tickets"}, authorizations.Authorizations[0].SupportedTargets, "one authorization serves every declared target")
	id := authorizations.Authorizations[0].Id

	docs := f.supplyTarget(t, owner, id, "docs")
	tickets := f.supplyTarget(t, owner, id, "tickets")
	require.Equal(t, []string{"docs.example.com"}, docs.Hosts)
	require.Equal(t, []string{"*.tickets.example.com"}, tickets.Hosts)
	require.Empty(t, docs.Paths, "a target declaring no paths or methods takes them all")
	require.Equal(t, []string{"/api/*"}, tickets.Paths, "a target's credential travels with its declared paths and methods")
	require.Equal(t, []string{"GET", "POST"}, tickets.Methods)
	require.NotZero(t, docs.ExpiresAtUnixMs, "the declared expiry is the credential's own")
	for _, supplied := range []*marketplaceprivatev1.SupplyServiceCredentialResponse{docs, tickets} {
		for _, service := range []string{"service-docs", "service-tickets"} {
			require.True(t, f.accepts(t, service, supplied.Token), "%s accepts the credential", service)
		}
	}
	_, err = f.supply.SupplyServiceCredential(trusted(ctx), &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: owner, AuthorizationId: id, Target: "github-api"})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a target the Provider does not declare gets nothing")

	require.NoError(t, f.credentials.RenewAuthorization(ctx, id), "the credential expires within the proactive window")
	renewed := f.supplyTarget(t, owner, id, "docs")
	require.NotEqual(t, docs.Token, renewed.Token)
	require.True(t, f.accepts(t, "service-docs", renewed.Token), "renewal keeps the targets' credential valid")
	require.True(t, f.accepts(t, "service-tickets", f.supplyTarget(t, owner, id, "tickets").Token))
	require.True(t, strings.HasPrefix(f.refreshCiphertext(t, id), "v1:"), "the refresh credential stays encrypted at rest")
}

//go:build integration

package infrastructure_test

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"github.com/talgent-work/workbench/internal/testsupport/providertest"
	"xorm.io/xorm"
)

// The Keycloak realm scripts/test/keycloak/realm.json declares: a confidential
// client requiring PKCE whose tokens are meant for two internal services.
const (
	keycloakClient   = "workbench"
	keycloakSecret   = "fixture-only"
	keycloakRedirect = "http://workbench.test/profile"
	credentialToken  = "credential-test"
)

// enterpriseIdP declares the realm as an enterprise identity provider: one
// authorization serves the docs and the ticket services, whose credentials
// expire.
func enterpriseIdP() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: "example-sso", DisplayName: "Example SSO", Derivation: domain.SharedTargetCredential, CredentialsExpire: true,
		Targets: []domain.ProviderTarget{{Name: "docs", Hosts: []domain.HostPattern{"docs.example.com"}}, {Name: "tickets", Hosts: []domain.HostPattern{"*.tickets.example.com"}}}}
}

type keycloakFixture struct {
	issuer      string
	public      marketplacepublicv1connect.MarketplaceServiceClient
	supply      marketplaceprivatev1connect.CredentialServiceClient
	credentials *application.ProviderCredentials
	db          *xorm.Engine
}

func newKeycloakFixture(t *testing.T) *keycloakFixture {
	t.Helper()
	issuer := middlewaretest.Environment(t, "TALGENT_TEST_KEYCLOAK_URL")
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := credentialcipher.New("keycloak test cipher key")
	require.NoError(t, err)
	client := oauthprovider.Client{Provider: "example-sso", ClientID: keycloakClient, ClientSecret: keycloakSecret, Scopes: []string{"openid"},
		AuthorizationEndpoint: issuer + "/protocol/openid-connect/auth", TokenEndpoint: issuer + "/protocol/openid-connect/token",
		RevocationEndpoint: issuer + "/protocol/openid-connect/revoke"}
	authorizer, err := oauthprovider.NewAuthorizer([]oauthprovider.Client{client}, cipher, nil)
	require.NoError(t, err)
	port, err := oauthprovider.NewPort(client, cipher, nil)
	require.NoError(t, err)
	providers := providertest.Declarations{enterpriseIdP()}
	store := persistence.NewProviderAuthorizations(db)
	credentials := application.NewProviderCredentials(store, providers, singlePort{port}, cipher,
		application.CredentialTiming{RequestSkew: 5 * time.Minute, ProactiveWindow: 30 * time.Minute, LeaseDuration: 30 * time.Second})
	connections := application.NewPersonalConnections(application.ConnectionDependencies{Authorizations: store, Queries: persistence.NewProviderAuthorizationQueries(db),
		Providers: providers, OAuth: authorizer, Credentials: credentials})
	app := application.NewApplication(nil, nil, nil, persistence.NewProviderAuthorizationQueries(db), connections, application.ConnectorRepositories{})
	mux := http.NewServeMux()
	path, handler := "/"+marketplacepublicv1connect.MarketplaceServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplacepublicv1connect.RegisterMarketplaceServiceHandler(s, transport.NewHandler(app, application.NewOwners(nil)))
	}, authn.NewPrincipalInterceptor())
	mux.Handle(path, handler)
	supplyPath, supplyHandler := "/"+marketplaceprivatev1connect.CredentialServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplaceprivatev1connect.RegisterCredentialServiceHandler(s, transport.NewCredentialHandler(connections, credentialToken))
	})
	mux.Handle(supplyPath, supplyHandler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	transportClient := connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL))
	return &keycloakFixture{issuer: issuer, public: marketplacepublicv1connect.NewMarketplaceServiceClient(transportClient),
		supply: marketplaceprivatev1connect.NewCredentialServiceClient(transportClient), credentials: credentials, db: db}
}

// One authorization at an enterprise identity provider, established through
// the authorization code flow with PKCE, serves both declared targets with a
// credential the provider accepts, confined to each target's hosts, and
// renewal keeps both valid.
func TestKeycloakAuthorizationServesEveryDeclaredTarget(t *testing.T) {
	f := newKeycloakFixture(t)
	ctx := t.Context()
	owner := "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c71"

	begun, err := f.public.BeginProviderAuthorization(as(ctx, owner), &marketplacepublicv1.BeginProviderAuthorizationRequest{Provider: "example-sso", RedirectUri: keycloakRedirect})
	require.NoError(t, err)
	authorization, err := url.Parse(begun.AuthorizationUrl)
	require.NoError(t, err)
	require.Equal(t, "S256", authorization.Query().Get("code_challenge_method"))
	code, state := signIn(t, begun.AuthorizationUrl, "alice", "fixture-only")
	completed, err := f.public.CompleteProviderAuthorization(as(ctx, owner), &marketplacepublicv1.CompleteProviderAuthorizationRequest{Provider: "example-sso", Code: code, State: state, RedirectUri: keycloakRedirect})
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
	var refresh string
	_, err = f.db.SQL("SELECT refresh_token_ciphertext FROM scm_provider_authorization WHERE id = ?", id).Get(&refresh)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(refresh, "v1:"), "the refresh credential stays encrypted at rest")
}

func (f *keycloakFixture) supplyTarget(t *testing.T, owner, id, target string) *marketplaceprivatev1.SupplyServiceCredentialResponse {
	t.Helper()
	supplied, err := f.supply.SupplyServiceCredential(trusted(t.Context()), &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: owner, AuthorizationId: id, Target: target})
	require.NoError(t, err)
	return supplied
}

// accepts is the service as a resource server: it introspects the token,
// which Keycloak answers only for a service in the token's audience.
func (f *keycloakFixture) accepts(t *testing.T, service, token string) bool {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.issuer+"/protocol/openid-connect/token/introspect", strings.NewReader(url.Values{"token": {token}}.Encode()))
	require.NoError(t, err)
	request.SetBasicAuth(service, keycloakSecret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	var result struct {
		Active bool `json:"active"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result.Active
}

var formAction = regexp.MustCompile(`action="([^"]+)"`)

// signIn is the person's browser at Keycloak: it opens the authorization
// address, submits the sign-in form and reads the code the redirect carries.
// Keycloak's cookies are Secure, so they are carried by hand over HTTP.
func signIn(t *testing.T, address, username, password string) (string, string) {
	t.Helper()
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	page, err := browser.Get(address)
	require.NoError(t, err)
	body, err := io.ReadAll(page.Body)
	_ = page.Body.Close()
	require.NoError(t, err)
	match := formAction.FindSubmatch(body)
	require.NotNil(t, match, "Keycloak shows its sign-in form")
	var cookies []string
	for _, cookie := range page.Cookies() {
		cookies = append(cookies, cookie.Name+"="+cookie.Value)
	}
	form, err := http.NewRequest(http.MethodPost, html.UnescapeString(string(match[1])), strings.NewReader(url.Values{"username": {username}, "password": {password}}.Encode()))
	require.NoError(t, err)
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	form.Header.Set("Cookie", strings.Join(cookies, "; "))
	redirected, err := browser.Do(form)
	require.NoError(t, err)
	_ = redirected.Body.Close()
	require.Equal(t, http.StatusFound, redirected.StatusCode)
	location, err := url.Parse(redirected.Header.Get("Location"))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(location.String(), keycloakRedirect))
	return location.Query().Get("code"), location.Query().Get("state")
}

func trusted(ctx context.Context) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+credentialToken)
	return ctx
}

type singlePort struct {
	port domain.ProviderAuthorizationPort
}

func (p singlePort) For(domain.Provider) (domain.ProviderAuthorizationPort, bool) {
	return p.port, true
}

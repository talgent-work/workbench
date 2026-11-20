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
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/credentialcipher"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/oauthprovider"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"github.com/talgent-work/workbench/internal/testsupport/providertest"
	"xorm.io/xorm"
)

// Both test authorization servers know Workbench as one confidential client
// requiring PKCE, and each resource server as a client that introspects.
const (
	oauthClientID   = "workbench"
	oauthSecret     = "fixture-only"
	oauthRedirect   = "http://workbench.test/profile"
	credentialToken = "credential-test"
)

// oauthFixture is Marketplace's credential supply for one declared OAuth
// Provider at a real authorization server, served over its public and
// trusted APIs with real MySQL.
type oauthFixture struct {
	introspection string
	public        marketplacepublicv1connect.MarketplaceServiceClient
	supply        marketplaceprivatev1connect.CredentialServiceClient
	credentials   *application.ProviderCredentials
	db            *xorm.Engine
}

func newOAuthFixture(t *testing.T, client oauthprovider.Client, declaration domain.ProviderDeclaration, introspection string) *oauthFixture {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	cipher, err := credentialcipher.New("oauth test cipher key")
	require.NoError(t, err)
	authorizer, err := oauthprovider.NewAuthorizer([]oauthprovider.Client{client}, cipher, nil)
	require.NoError(t, err)
	port, err := oauthprovider.NewPort(client, cipher, nil)
	require.NoError(t, err)
	providers := providertest.Declarations{declaration}
	store := persistence.NewProviderAuthorizations(db)
	credentials := application.NewProviderCredentials(store, providers, singlePort{port}, cipher,
		application.CredentialTiming{RequestSkew: 5 * time.Minute, ProactiveWindow: 30 * time.Minute, LeaseDuration: 30 * time.Second})
	connections := application.NewPersonalConnections(application.ConnectionDependencies{Authorizations: store, Queries: persistence.NewProviderAuthorizationQueries(db),
		Providers: providers, OAuth: authorizer, Credentials: credentials})
	app := application.NewApplication(nil, nil, nil, persistence.NewProviderAuthorizationQueries(db), connections, application.ConnectorDependencies{})
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
	return &oauthFixture{introspection: introspection, public: marketplacepublicv1connect.NewMarketplaceServiceClient(transportClient),
		supply: marketplaceprivatev1connect.NewCredentialServiceClient(transportClient), credentials: credentials, db: db}
}

func (f *oauthFixture) supplyTarget(t *testing.T, owner, id, target string) *marketplaceprivatev1.SupplyServiceCredentialResponse {
	t.Helper()
	supplied, err := f.supply.SupplyServiceCredential(trusted(t.Context()), &marketplaceprivatev1.SupplyServiceCredentialRequest{OwnerUserId: owner, AuthorizationId: id, Target: target})
	require.NoError(t, err)
	return supplied
}

// introspection is what a resource server learns of a token (RFC 7662).
type introspection struct {
	Active   bool `json:"active"`
	Audience any  `json:"aud"`
}

// introspect is the service as a resource server: it introspects the token,
// which the authorization server answers only for a service in the token's
// audience.
func (f *oauthFixture) introspect(t *testing.T, service, token string) introspection {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.introspection, strings.NewReader(url.Values{"token": {token}}.Encode()))
	require.NoError(t, err)
	request.SetBasicAuth(service, oauthSecret)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var result introspection
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return result
}

func (f *oauthFixture) accepts(t *testing.T, service, token string) bool {
	t.Helper()
	return f.introspect(t, service, token).Active
}

// refreshCiphertext is the authorization's refresh credential as stored.
func (f *oauthFixture) refreshCiphertext(t *testing.T, id string) string {
	t.Helper()
	var refresh string
	_, err := f.db.SQL("SELECT refresh_token_ciphertext FROM scm_provider_authorization WHERE id = ?", id).Get(&refresh)
	require.NoError(t, err)
	return refresh
}

var (
	formAction = regexp.MustCompile(`<form[^>]*action="([^"]+)"`)
	formInput  = regexp.MustCompile(`<input[^>]*name="([^"]+)"[^>]*>`)
	inputValue = regexp.MustCompile(`value="([^"]*)"`)
)

// signIn is the person's browser at the authorization server: it follows
// redirects from the authorization address, fills each sign-in form it is
// shown with the person's login and password, keeping the form's hidden
// fields, and reads the code the redirect to Workbench carries. The servers'
// cookies may be Secure, so they are carried by hand over HTTP.
func signIn(t *testing.T, address, login, password string) (string, string) {
	t.Helper()
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	cookies := map[string]string{}
	visit := func(method, target string, form url.Values) (*http.Response, string) {
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
		}
		request, err := http.NewRequestWithContext(t.Context(), method, target, body)
		require.NoError(t, err)
		if form != nil {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		var jar []string
		for name, value := range cookies {
			jar = append(jar, name+"="+value)
		}
		request.Header.Set("Cookie", strings.Join(jar, "; "))
		response, err := browser.Do(request)
		require.NoError(t, err)
		page, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		require.NoError(t, err)
		for _, cookie := range response.Cookies() {
			cookies[cookie.Name] = cookie.Value
		}
		return response, string(page)
	}
	current, method, form := address, http.MethodGet, url.Values(nil)
	for range 6 {
		response, page := visit(method, current, form)
		method, form = http.MethodGet, nil
		if location := response.Header.Get("Location"); location != "" {
			next, err := url.Parse(current)
			require.NoError(t, err)
			next, err = next.Parse(location)
			require.NoError(t, err)
			if strings.HasPrefix(next.String(), oauthRedirect) {
				return next.Query().Get("code"), next.Query().Get("state")
			}
			current = next.String()
			continue
		}
		action := formAction.FindStringSubmatch(page)
		require.NotNil(t, action, "the authorization server shows its sign-in form")
		form = url.Values{}
		for _, input := range formInput.FindAllStringSubmatch(page, -1) {
			value := ""
			if match := inputValue.FindStringSubmatch(input[0]); match != nil {
				value = html.UnescapeString(match[1])
			}
			switch input[1] {
			case "username", "login":
				value = login
			case "password":
				value = password
			}
			form.Set(input[1], value)
		}
		target, err := url.Parse(current)
		require.NoError(t, err)
		target, err = target.Parse(html.UnescapeString(action[1]))
		require.NoError(t, err)
		current, method = target.String(), http.MethodPost
	}
	t.Fatal("the authorization server never redirected to Workbench")
	return "", ""
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

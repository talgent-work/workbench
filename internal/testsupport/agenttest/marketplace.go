package agenttest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	marketapp "github.com/talgent-work/workbench/internal/business/marketplace/application"
	marketdomain "github.com/talgent-work/workbench/internal/business/marketplace/domain"
	marketinfra "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	marketstore "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure/persistence"
	markettransport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// SecretCanary is the access token every Marketplace authorization holds: no
// response and no Work may carry it.
const SecretCanary = "reference-query-secret-canary"

// Authorization is a user's GitHub authorization in the Marketplace.
type Authorization struct {
	User string
	// Stale is an authorization that needs reconnecting; it offers nothing.
	Stale bool
}

// Marketplace serves the Workbench's Marketplace over the MySQL schema at dsn,
// with the given authorizations, until the test ends: each user reaches one
// repository of their own, "github:<user>" named "<user>/project". It returns
// its address, for Config.Marketplace, and each user's authorization ID.
func Marketplace(t testing.TB, dsn string, authorizations ...Authorization) (string, map[string]string) {
	t.Helper()
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	store := marketstore.NewProviderAuthorizations(db)
	ids := map[string]string{}
	for _, granted := range authorizations {
		authorization, err := marketdomain.NewProviderAuthorization(marketdomain.ProviderAuthorizationInput{
			OwnerUserID: granted.User, Provider: marketdomain.ProviderGitHub,
			ProviderAccountID: "101", ProviderLogin: "fixture",
			AccessTokenCiphertext: SecretCanary,
		})
		require.NoError(t, err)
		if granted.Stale {
			require.NoError(t, authorization.MarkReconnectRequired("fixture", "Reconnect required", time.Now()))
		}
		require.NoError(t, store.Save(ctx, authorization))
		ids[granted.User] = authorization.ID
	}
	personal := marketapp.NewPersonalConnections(store, ownRepository{}, nil, nil, nil, nil, nil, nil, nil)
	for _, id := range ids {
		require.NoError(t, personal.ObserveAccessScope(ctx, id))
	}
	market := marketapp.NewApplication(nil, nil, nil, marketinfra.NewProviderAuthorizationQueries(db), personal, marketapp.ConnectorRepositories{})
	marketplace := connect.NewServer(authn.NewPrincipalInterceptor())
	marketplacepublicv1connect.RegisterMarketplaceServiceHandler(marketplace, markettransport.NewHandler(market, marketapp.NewOwners(noApplications{})))
	mux := http.NewServeMux()
	connecthttp.Mount(mux, marketplace)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL, ids
}

// noApplications answers that no Application exists: Agent reaches
// Marketplace for a delegated owner and never names an Application.
type noApplications struct{}

func (noApplications) GetApplication(context.Context, string) (marketapp.OwnedApplication, error) {
	return marketapp.OwnedApplication{}, marketapp.ErrApplicationUnknown
}

// ownRepository stands in for the Provider's live decision: each user reaches
// one repository of their own.
type ownRepository struct{}

func (ownRepository) ObserveAccess(_ context.Context, owner string, _ marketdomain.Provider) (marketdomain.AccessScope, error) {
	return marketdomain.AccessScope{ObservedAt: time.Now(), Accounts: []marketdomain.ScopeAccount{{InstallationID: "1", Login: owner, Repositories: []marketdomain.ScopeRepository{{
		RepoID: "github:" + owner, RepoName: owner + "/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}}}}}, nil
}

func (ownRepository) RequestedPermissions(context.Context, marketdomain.Provider) ([]marketapp.ProviderPermission, error) {
	return nil, nil
}

func (ownRepository) ManageURL(context.Context, marketdomain.Provider) (string, error) {
	return "", nil
}

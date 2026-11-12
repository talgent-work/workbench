//go:build integration

package persistence

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	marketinfra "github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// knownApplications is Account's ownership answer in these tests.
type knownApplications map[string]application.OwnedApplication

func (k knownApplications) GetApplication(_ context.Context, id string) (application.OwnedApplication, error) {
	found, ok := k[id]
	if !ok {
		return application.OwnedApplication{}, application.ErrApplicationUnknown
	}
	return found, nil
}

func asApplication(ctx context.Context, id string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderPrincipal, "application")
	info.RequestHeader().Set(authn.HeaderSubject, id)
	return ctx
}

// An Application's Provider Authorizations are its own: its Application
// Owner connects, reads and revokes them under their own identity, and the
// Application reads and revokes them as itself, while the owner's own
// authorizations, other people and other Applications stay apart
// (Application as owner, Application Owner manages).
func TestAnApplicationOwnerManagesTheApplicationsAuthorizations(t *testing.T) {
	ctx := t.Context()
	container, err := mysqltest.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Release(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "parseTime=true&multiStatements=true")
	require.NoError(t, err)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	const (
		owner   = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c51"
		other   = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c52"
		desk    = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
		billing = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4f"
		retired = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c50"
	)
	repo := NewProviderAuthorizations(db)
	own := newProviderAuthorizationForTest(t, owner)
	require.NoError(t, repo.Save(ctx, own))
	port := &cancellationAuthority{entered: make(chan struct{}, 2), confirm: make(chan bool, 2)}
	store := application.NewCredentialChangeDispatch(repo, &credentialChanges{}, slog.Default())
	connections := application.NewPersonalConnections(store, unobservableAccess{}, nil, &reauthorizationAuthority{}, nil, port, repo, nil, NewAuthorizationMaintenanceQueries(db))
	app := application.NewApplication(nil, nil, nil, marketinfra.NewProviderAuthorizationQueries(db), connections, application.ConnectorRepositories{})
	owners := application.NewOwners(knownApplications{
		desk:    {ID: desk, OwnerUserID: owner},
		billing: {ID: billing, OwnerUserID: other},
		retired: {ID: retired, OwnerUserID: owner, Removed: true},
	})
	path, handler := "/"+marketplacepublicv1connect.MarketplaceServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplacepublicv1connect.RegisterMarketplaceServiceHandler(s, transport.NewHandler(app, owners))
	}, authn.NewPrincipalInterceptor())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := marketplacepublicv1connect.NewMarketplaceServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))

	// The owner connects GitHub for the Application through the browser step.
	connected, err := client.CompleteProviderAuthorization(personal(ctx, owner), &marketplacepublicv1.CompleteProviderAuthorizationRequest{
		Provider: "github", Code: "code", State: "state", ApplicationId: desk,
	})
	require.NoError(t, err)
	require.Equal(t, desk, connected.GetAuthorization().GetOwnerUserId())
	deskAuthorization := connected.GetAuthorization().GetId()
	require.NotEqual(t, own.ID, deskAuthorization, "the Application's authorization is not its owner's")

	listed := func(ctx context.Context, applicationID string) ([]string, error) {
		response, err := client.ListPersonalAuthorizations(ctx, &marketplacepublicv1.ListPersonalAuthorizationsRequest{ApplicationId: applicationID})
		var ids []string
		for _, authorization := range response.GetAuthorizations() {
			ids = append(ids, authorization.GetId())
		}
		return ids, err
	}
	ids, err := listed(personal(ctx, owner), desk)
	require.NoError(t, err)
	require.Equal(t, []string{deskAuthorization}, ids)
	ids, err = listed(personal(ctx, owner), "")
	require.NoError(t, err)
	require.Equal(t, []string{own.ID}, ids, "the owner's own stay apart")
	ids, err = listed(asApplication(ctx, desk), "")
	require.NoError(t, err)
	require.Equal(t, []string{deskAuthorization}, ids, "the Application reads its own as itself")
	state, err := client.GetProviderConnectionState(asApplication(ctx, desk), &marketplacepublicv1.GetProviderConnectionStateRequest{Provider: "github"})
	require.NoError(t, err)
	require.Equal(t, deskAuthorization, state.GetState().GetAuthorizationId())

	denied := func(err error) { t.Helper(); require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err)) }
	_, err = listed(personal(ctx, other), desk)
	denied(err)
	_, err = listed(personal(ctx, owner), billing)
	denied(err)
	_, err = listed(personal(ctx, owner), retired)
	denied(err)
	_, err = listed(personal(ctx, owner), "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c99")
	denied(err)
	_, err = listed(asApplication(ctx, billing), desk)
	denied(err)
	_, err = client.BeginProviderAuthorization(personal(ctx, other), &marketplacepublicv1.BeginProviderAuthorizationRequest{Provider: "github", ApplicationId: desk})
	denied(err)
	_, err = client.CancelPersonalAuthorization(asApplication(ctx, billing), &marketplacepublicv1.CancelPersonalAuthorizationRequest{AuthorizationId: deskAuthorization})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "another Application finds no such authorization")
	_, err = client.CancelPersonalAuthorization(personal(ctx, owner), &marketplacepublicv1.CancelPersonalAuthorizationRequest{AuthorizationId: deskAuthorization})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err), "the owner revokes it only by naming the Application")
	require.Zero(t, port.revocations.Load())

	// The owner revokes it for the Application; the owner's own stays.
	port.confirm <- true
	revoked, err := client.CancelPersonalAuthorization(personal(ctx, owner), &marketplacepublicv1.CancelPersonalAuthorizationRequest{AuthorizationId: deskAuthorization, ApplicationId: desk})
	require.NoError(t, err)
	require.Equal(t, "revoked", revoked.GetAuthorization().GetStatus())
	select {
	case <-port.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation was not invoked")
	}
	kept, err := repo.Get(ctx, own.ID)
	require.NoError(t, err)
	require.NotEqual(t, "revoked", string(kept.Status))
}

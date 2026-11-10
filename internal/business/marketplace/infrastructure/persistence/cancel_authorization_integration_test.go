//go:build integration

package persistence

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

type cancellationAuthority struct {
	entered     chan struct{}
	confirm     chan bool
	renewals    atomic.Int32
	revocations atomic.Int32
}

type reauthorizationAuthority struct{ calls atomic.Int32 }

func (*reauthorizationAuthority) BeginProviderAuthorization(context.Context, application.BeginProviderAuthorizationInput) (application.BeginProviderAuthorizationResult, error) {
	return application.BeginProviderAuthorizationResult{}, nil
}

func (p *reauthorizationAuthority) CompleteProviderAuthorization(_ context.Context, input application.CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	p.calls.Add(1)
	return domain.NewProviderAuthorization(domain.ProviderAuthorizationInput{
		OwnerUserID: input.OwnerUserID, Provider: input.Provider,
		ProviderAccountID: "replacement-account", ProviderLogin: "replacement-login",
		AccessTokenCiphertext: "replacement-protected-access",
	})
}

func (p *cancellationAuthority) Renew(context.Context, domain.CredentialSet) (domain.CredentialRenewal, error) {
	p.renewals.Add(1)
	return domain.CredentialRenewal{Credentials: domain.CredentialSet{
		AccessTokenCiphertext: "rotated-protected-access", RefreshTokenCiphertext: "rotated-protected-refresh",
		ExpiresAt: time.Now().Add(time.Hour), RefreshTokenExpiresAt: time.Now().Add(24 * time.Hour),
	}}, nil
}

func (p *cancellationAuthority) Revoke(ctx context.Context, _ domain.CredentialSet) (bool, error) {
	p.revocations.Add(1)
	p.entered <- struct{}{}
	select {
	case confirmed := <-p.confirm:
		return confirmed, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func TestCancelPersonalAuthorizationPersistsProviderOutcomeAndFencesRenewal(t *testing.T) {
	ctx := t.Context()
	container, err := mysqltest.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Release(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "parseTime=true&multiStatements=true")
	require.NoError(t, err)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := NewProviderAuthorizations(db)
	owner, other := "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "cfe58e71-efc6-5336-8009-449ad568c42b"
	auth := newProviderAuthorizationForTest(t, owner)
	auth.ExpiresAt = time.Now().Add(-time.Minute)
	require.NoError(t, repo.Save(ctx, auth))
	port := &cancellationAuthority{entered: make(chan struct{}, 2), confirm: make(chan bool, 2)}
	reauthorization := &reauthorizationAuthority{}
	changes := &credentialChanges{}
	store := application.NewCredentialChangeDispatch(repo, changes, slog.Default())
	connections := application.NewPersonalConnections(store, unobservableAccess{}, nil, reauthorization, nil, port, repo, nil, NewAuthorizationMaintenanceQueries(db))
	app := application.NewApplication(nil, nil, nil, nil, connections, application.ConnectorRepositories{})
	path, handler := "/"+marketplacepublicv1connect.MarketplaceServiceName+"/", connecttest.Handler(func(s *connect.Server) {
		marketplacepublicv1connect.RegisterMarketplaceServiceHandler(s, transport.NewHandler(app))
	}, authn.NewPrincipalInterceptor())
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	client := marketplacepublicv1connect.NewMarketplaceServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)))
	state, err := client.GetProviderConnectionState(personal(ctx, owner), &marketplacepublicv1.GetProviderConnectionStateRequest{Provider: "github"})
	require.NoError(t, err)
	require.Equal(t, auth.ID, state.State.AuthorizationId)
	foreignState, err := client.GetProviderConnectionState(personal(ctx, other), &marketplacepublicv1.GetProviderConnectionStateRequest{Provider: "github"})
	require.NoError(t, err)
	require.Equal(t, "none", foreignState.State.Status)
	require.Empty(t, foreignState.State.AuthorizationId)
	cancellation := &marketplacepublicv1.CancelPersonalAuthorizationRequest{AuthorizationId: auth.ID}
	_, err = client.CancelPersonalAuthorization(personal(ctx, ""), cancellation)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.CancelPersonalAuthorization(personal(ctx, other), cancellation)
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	require.Zero(t, port.revocations.Load())

	result := make(chan error, 1)
	go func() {
		_, err := client.CancelPersonalAuthorization(personal(ctx, owner), cancellation)
		result <- err
	}()
	select {
	case <-port.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation was not invoked")
	}
	current, err := repo.Get(ctx, auth.ID)
	require.NoError(t, err)
	_, err = repo.AcquireCredentialLease(ctx, current, "competing-renewal", time.Now().Add(time.Minute), time.Now())
	require.ErrorIs(t, err, domain.ErrConcurrentModification)
	reconnectRequest := &marketplacepublicv1.CompleteProviderAuthorizationRequest{Provider: "github", Code: "test-code", State: "test-state"}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
	info.RequestHeader().Set(authn.HeaderPrincipal, "user")
	info.RequestHeader().Set(authn.HeaderSubject, owner)
	_, err = client.CompleteProviderAuthorization(ctx, reconnectRequest)
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "reconnection must wait for the in-flight credential operation")
	require.Zero(t, reauthorization.calls.Load(), "a blocked operation must not consume the OAuth callback")
	port.confirm <- false
	require.Equal(t, connect.CodeUnavailable, connect.CodeOf(<-result))
	current, err = repo.Get(ctx, auth.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderAuthorizationActive, current.Status)
	require.Equal(t, "rotated-protected-access", current.AccessTokenCiphertext, "failed revocation must preserve completed renewal")
	require.EqualValues(t, 1, port.renewals.Load())
	require.Equal(t, []domain.CredentialChange{domain.CredentialRenewed}, changes.take(), "the persisted renewal is published")

	// Completion released the lease, allowing cancellation to be retried now.
	port.confirm <- true
	response, err := client.CancelPersonalAuthorization(personal(ctx, owner), cancellation)
	require.NoError(t, err)
	require.Equal(t, auth.ID, response.Authorization.Id)
	require.Equal(t, "revoked", response.Authorization.Status)
	current, err = repo.Get(ctx, auth.ID)
	require.NoError(t, err)
	require.Equal(t, domain.ProviderAuthorizationRevoked, current.Status)
	require.Equal(t, []domain.CredentialChange{domain.CredentialRevoked}, changes.take())
	_, err = client.CancelPersonalAuthorization(personal(ctx, owner), cancellation)
	require.NoError(t, err)
	require.EqualValues(t, 2, port.revocations.Load(), "repeat cancellation must not invoke the Provider again")
	require.Empty(t, changes.take(), "a repeated revocation publishes nothing")
	reauthorized, err := client.CompleteProviderAuthorization(ctx, reconnectRequest)
	require.NoError(t, err)
	require.Equal(t, auth.ID, reauthorized.Authorization.Id)
	require.Equal(t, "active", reauthorized.Authorization.Status)
	reconnected, err := repo.GetByOwnerAndProvider(ctx, owner, domain.ProviderGitHub)
	require.NoError(t, err)
	require.Equal(t, auth.ID, reconnected.ID)
	require.Equal(t, "replacement-account", reconnected.ProviderAccountID)
	require.Equal(t, domain.ProviderAuthorizationActive, reconnected.Status)
	require.Equal(t, []domain.CredentialChange{domain.CredentialReauthorized}, changes.take())

	// An unusable refresh token follows the same existing-account OAuth flow.
	// The public query must offer reconnection, then clear the previous failure
	// without replacing the identity retained by existing Works.
	require.NoError(t, reconnected.MarkReconnectRequired("github_reconnect_required", "provider requires reauthorization", time.Now()))
	require.NoError(t, repo.Save(ctx, reconnected))
	state, err = client.GetProviderConnectionState(personal(ctx, owner), &marketplacepublicv1.GetProviderConnectionStateRequest{Provider: "github"})
	require.NoError(t, err)
	require.Equal(t, "reconnect_required", state.State.Status)
	require.Equal(t, "reconnect", state.State.RecommendedAction)
	reauthorized, err = client.CompleteProviderAuthorization(ctx, reconnectRequest)
	require.NoError(t, err)
	require.Equal(t, auth.ID, reauthorized.Authorization.Id)
	state, err = client.GetProviderConnectionState(personal(ctx, owner), &marketplacepublicv1.GetProviderConnectionStateRequest{Provider: "github"})
	require.NoError(t, err)
	require.Equal(t, "active", state.State.Status)
	require.Empty(t, state.State.ReasonCode)
	require.True(t, state.State.CanListRepositories)
	reconnected, err = repo.Get(ctx, auth.ID)
	require.NoError(t, err)
	credentials, err := reconnected.SupplyValidCredentials(ctx, domain.CredentialRequirement{OwnerUserID: owner, Provider: domain.ProviderGitHub, Applicable: true}, time.Now(), 0, port)
	require.NoError(t, err)
	require.Equal(t, "replacement-protected-access", credentials.AccessTokenCiphertext)

	// A crashed worker is fenced until expiry; the current version can then
	// acquire again, while stale versions cannot overwrite the recovered grant.
	second := newProviderAuthorizationForTest(t, other)
	require.NoError(t, repo.Save(ctx, second))
	second, err = repo.Get(ctx, second.ID)
	require.NoError(t, err)
	now := time.Now().UTC().Truncate(time.Millisecond)
	leased, err := repo.AcquireCredentialLease(ctx, second, "lost-worker", now.Add(time.Second), now)
	require.NoError(t, err)
	recovered, err := repo.AcquireCredentialLease(ctx, leased, "recovery", now.Add(time.Minute), now.Add(2*time.Second))
	require.NoError(t, err)
	require.ErrorIs(t, repo.Save(ctx, leased), domain.ErrConcurrentModification)
	require.NoError(t, recovered.MarkRevoked(now))
	require.NoError(t, repo.Save(ctx, recovered))
}

// personal is the context of a call the gateway made for actor; an empty
// actor signs no one in.
func personal(ctx context.Context, actor string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	if actor != "" {
		info.RequestHeader().Set(authn.HeaderAuthenticatedBy, "envoy")
		info.RequestHeader().Set(authn.HeaderPrincipal, "user")
		info.RequestHeader().Set(authn.HeaderSubject, actor)
	}
	return ctx
}

// unobservableAccess is a Provider whose reach cannot be observed; observation
// is outside this test.
type unobservableAccess struct{}

func (unobservableAccess) ObserveAccess(context.Context, string, domain.Provider) (domain.AccessScope, error) {
	return domain.AccessScope{}, application.ErrProviderUnavailable
}

func (unobservableAccess) RequestedPermissions(context.Context, domain.Provider) ([]application.ProviderPermission, error) {
	return nil, nil
}

func (unobservableAccess) ManageURL(context.Context, domain.Provider) (string, error) { return "", nil }

// credentialChanges records the credential changes dispatched after saves.
type credentialChanges struct {
	mu      sync.Mutex
	changes []domain.CredentialChange
}

func (c *credentialChanges) Dispatch(ctx context.Context, event domainevent.Event) error {
	return c.DispatchAll(ctx, []domainevent.Event{event})
}

func (c *credentialChanges) DispatchAll(_ context.Context, events []domainevent.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, event := range events {
		c.changes = append(c.changes, event.(domain.CredentialChanged).Change)
	}
	return nil
}

func (*credentialChanges) Close(context.Context) error { return nil }

func (c *credentialChanges) take() []domain.CredentialChange {
	c.mu.Lock()
	defer c.mu.Unlock()
	taken := c.changes
	c.changes = nil
	return taken
}

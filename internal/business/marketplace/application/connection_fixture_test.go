package application

import (
	"context"
	"errors"
	"time"

	"sort"
	"testing"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

type testApplication struct {
	*PersonalConnections
	authorizations *fakeAuthorizationRepository
	observer       *fakeProviderObserver
	observations   *recordingObservations
	authorizer     *fakeProviderAuthorizer
	gitHub         *fakeGitHub
	provider       *fakeProviderPort
}

// fakeGitHub is GitHub as the tests see it: a live access decision, an
// authorizer and, when a test sets one, a repository credential supplier.
type fakeGitHub struct {
	observer   *fakeProviderObserver
	authorizer *fakeProviderAuthorizer
	supplier   *recordingRepositorySupplier
}

func (g *fakeGitHub) BeginProviderAuthorization(ctx context.Context, input BeginProviderAuthorizationInput) (BeginProviderAuthorizationResult, error) {
	return g.authorizer.BeginProviderAuthorization(ctx, input)
}

func (g *fakeGitHub) CompleteProviderAuthorization(ctx context.Context, input CompleteProviderAuthorizationInput) (*domain.ProviderAuthorization, error) {
	return g.authorizer.CompleteProviderAuthorization(ctx, input)
}

func (g *fakeGitHub) ObserveAccess(ctx context.Context, token string) (domain.AccessScope, error) {
	return g.observer.ObserveAccess(ctx, token)
}

func (g *fakeGitHub) RequestedPermissions(ctx context.Context, provider domain.Provider) ([]ProviderPermission, error) {
	return g.observer.RequestedPermissions(ctx, provider)
}

func (g *fakeGitHub) ManageURL(ctx context.Context, provider domain.Provider) (string, error) {
	return g.observer.ManageURL(ctx, provider)
}

func (g *fakeGitHub) SupplyRepositoryCredential(ctx context.Context, cloneURL, token string, expiresAt time.Time) (credential.MaterializedCredential, error) {
	if g.supplier == nil {
		return credential.MaterializedCredential{}, errors.New("fake GitHub: repository credential not expected")
	}
	return g.supplier.SupplyRepositoryCredential(ctx, cloneURL, token, expiresAt)
}

func newTestApplication(t *testing.T) *testApplication {
	t.Helper()
	authorizations := &fakeAuthorizationRepository{authorizations: map[string]*domain.ProviderAuthorization{}}
	observer := &fakeProviderObserver{}
	observations := &recordingObservations{}
	authorizer := &fakeProviderAuthorizer{}
	gitHub := &fakeGitHub{observer: observer, authorizer: authorizer}
	provider := &fakeProviderPort{now: time.Now()}
	providers := staticProviders{gitHubDeclaration()}
	credentials := NewProviderCredentials(authorizations, providers, onePort{provider}, plainCipher{}, CredentialTiming{RequestSkew: 5 * time.Minute, LeaseDuration: 30 * time.Second})
	app := NewPersonalConnections(ConnectionDependencies{Authorizations: authorizations, Queries: authorizations, Providers: providers,
		GitHub: gitHub, Tasks: observations, Credentials: credentials})
	return &testApplication{PersonalConnections: app, authorizations: authorizations, observer: observer, observations: observations, authorizer: authorizer, gitHub: gitHub, provider: provider}
}

func testMaintenancePage(ids []string, after string) query.MaintenanceIdentities {
	sort.Strings(ids)
	page := query.MaintenanceIdentities{}
	for _, id := range ids {
		if id > after {
			page.IDs = append(page.IDs, id)
		}
	}
	if len(page.IDs) > 100 {
		page.IDs = page.IDs[:100]
		page.NextCursor = page.IDs[99]
	}
	return page
}

// staticProviders are the deployment's Provider declarations as a test
// declares them.
type staticProviders []domain.ProviderDeclaration

func (p staticProviders) Providers() []domain.ProviderDeclaration { return p }

func (p staticProviders) Provider(name domain.Provider) (domain.ProviderDeclaration, bool) {
	for _, declaration := range p {
		if declaration.Name == name {
			return declaration, true
		}
	}
	return domain.ProviderDeclaration{}, false
}

func gitHubDeclaration() domain.ProviderDeclaration {
	return domain.ProviderDeclaration{Name: domain.ProviderGitHub, DisplayName: "GitHub", Derivation: domain.SharedTargetCredential,
		CredentialsExpire: true, ObservesAccessScope: true, Targets: []domain.ProviderTarget{
			{Name: "github-api", Hosts: []domain.HostPattern{"api.github.com"}},
			{Name: "github-remote-mcp", Hosts: []domain.HostPattern{"api.githubcopilot.com"}},
		}}
}

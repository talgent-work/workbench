package application

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/marketplace/application/credential"
	"github.com/talgent-work/workbench/internal/business/marketplace/application/query"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
	"sort"
	"testing"
)

type testApplication struct {
	*PersonalConnections
	authorizations *fakeAuthorizationRepository
	observer       *fakeProviderObserver
	observations   *recordingObservations
	authorizer     *fakeProviderAuthorizer
	gitHub         *fakeGitHub
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

func (g *fakeGitHub) ObserveAccess(ctx context.Context, ownerID string, provider domain.Provider) (domain.AccessScope, error) {
	return g.observer.ObserveAccess(ctx, ownerID, provider)
}

func (g *fakeGitHub) RequestedPermissions(ctx context.Context, provider domain.Provider) ([]ProviderPermission, error) {
	return g.observer.RequestedPermissions(ctx, provider)
}

func (g *fakeGitHub) ManageURL(ctx context.Context, provider domain.Provider) (string, error) {
	return g.observer.ManageURL(ctx, provider)
}

func (g *fakeGitHub) RefreshProviderAuthorization(context.Context, string, string) error {
	return errors.New("fake GitHub: refresh not expected")
}

func (g *fakeGitHub) SupplyRepositoryCredential(ctx context.Context, ownerID, authorizationID, cloneURL string) (credential.MaterializedCredential, error) {
	if g.supplier == nil {
		return credential.MaterializedCredential{}, errors.New("fake GitHub: repository credential not expected")
	}
	return g.supplier.SupplyRepositoryCredential(ctx, ownerID, authorizationID, cloneURL)
}

func (g *fakeGitHub) SupplyServiceCredential(context.Context, string, string, string) (credential.ServiceMaterial, error) {
	return credential.ServiceMaterial{}, errors.New("fake GitHub: service credential not expected")
}

func newTestApplication(t *testing.T) *testApplication {
	t.Helper()
	authorizations := &fakeAuthorizationRepository{authorizations: map[string]*domain.ProviderAuthorization{}}
	observer := &fakeProviderObserver{}
	observations := &recordingObservations{}
	authorizer := &fakeProviderAuthorizer{}
	gitHub := &fakeGitHub{observer: observer, authorizer: authorizer}
	app := NewPersonalConnections(authorizations, authorizations, gitHub, observations, nil)
	return &testApplication{PersonalConnections: app, authorizations: authorizations, observer: observer, observations: observations, authorizer: authorizer, gitHub: gitHub}
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

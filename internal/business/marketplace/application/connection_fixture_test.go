package application

import (
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
}

func newTestApplication(t *testing.T) *testApplication {
	t.Helper()
	authorizations := &fakeAuthorizationRepository{authorizations: map[string]*domain.ProviderAuthorization{}}
	observer := &fakeProviderObserver{}
	observations := &recordingObservations{}
	authorizer := &fakeProviderAuthorizer{}
	app := NewPersonalConnections(authorizations, observer, observations, authorizer, nil, nil, authorizations, nil, authorizations)
	return &testApplication{PersonalConnections: app, authorizations: authorizations, observer: observer, observations: observations, authorizer: authorizer}
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

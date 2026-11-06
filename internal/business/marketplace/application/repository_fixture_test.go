package application

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// fakeProviderObserver is the Provider's live access decision: accounts
// (installations) with their repositories. observations counts every live
// request, so reads can be shown never to ask the Provider.
type fakeProviderObserver struct {
	accounts     []domain.ScopeAccount
	permissions  []domain.ScopePermission
	requested    []ProviderPermission
	observeErr   error
	observations atomic.Int32
	now          time.Time
}

func (c *fakeProviderObserver) ObserveAccess(context.Context, string, domain.Provider) (domain.AccessScope, error) {
	c.observations.Add(1)
	if c.observeErr != nil {
		return domain.AccessScope{}, c.observeErr
	}
	observedAt := c.now
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	return domain.AccessScope{Accounts: c.accounts, Permissions: c.permissions, ObservedAt: observedAt}, nil
}

func (c *fakeProviderObserver) RequestedPermissions(context.Context, domain.Provider) ([]ProviderPermission, error) {
	return c.requested, nil
}

func (c *fakeProviderObserver) ManageURL(context.Context, domain.Provider) (string, error) {
	return "https://github.example.test/apps/talgent/installations/new", nil
}

type recordingObservations struct{ scheduled []string }

func (r *recordingObservations) ScheduleObservation(_ context.Context, authorizationID string) error {
	r.scheduled = append(r.scheduled, authorizationID)
	return nil
}

func scopeAccount(installationID, login string, repositories ...domain.ScopeRepository) domain.ScopeAccount {
	return domain.ScopeAccount{InstallationID: installationID, Login: login, Type: "Organization", RepositorySelection: "selected", Repositories: repositories}
}

func githubRepository(id, name string) domain.ScopeRepository {
	return domain.ScopeRepository{RepoID: id, RepoName: name, CloneURL: "https://github.com/" + name + ".git", WebURL: "https://github.com/" + name, DefaultBranch: "main"}
}

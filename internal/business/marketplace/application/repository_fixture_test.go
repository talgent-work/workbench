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
	// rejected tokens are refused as GitHub refuses a revoked token.
	rejected map[string]bool
	tokens   []string
}

func (c *fakeProviderObserver) ObserveAccess(_ context.Context, token string) (domain.AccessScope, error) {
	c.observations.Add(1)
	c.tokens = append(c.tokens, token)
	if c.rejected[token] {
		return domain.AccessScope{}, ErrCredentialRejected
	}
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

func (r *recordingObservations) ScheduleAccessScopeObservation(_ context.Context, authorizationID string) error {
	r.scheduled = append(r.scheduled, authorizationID)
	return nil
}

func scopeAccount(installationID, login string, repositories ...domain.ScopeRepository) domain.ScopeAccount {
	return domain.ScopeAccount{InstallationID: installationID, Login: login, Type: "Organization", RepositorySelection: "selected", Repositories: repositories}
}

func githubRepository(id, name string) domain.ScopeRepository {
	return domain.ScopeRepository{RepoID: id, RepoName: name, CloneURL: "https://github.com/" + name + ".git", WebURL: "https://github.com/" + name, DefaultBranch: "main"}
}

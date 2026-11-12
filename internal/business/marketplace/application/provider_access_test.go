package application

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/marketplace/domain"
)

// Intent (real): reading the Provider page and the repository choices never
// asks the Provider; they show what was last observed.
func TestProviderReadsUseTheRetainedScopeWithoutAskingTheProvider(t *testing.T) {
	app := newTestApplication(t)
	authorization := mustAuthorization(t, "owner", "octocat")
	app.authorizations.save(authorization)
	shared := githubRepository("github:3", "acme/shared")
	app.observer.accounts = []domain.ScopeAccount{
		scopeAccount("1", "octocat", githubRepository("github:1", "octocat/dotfiles"), shared),
		scopeAccount("2", "acme", githubRepository("github:2", "acme/api"), shared),
	}

	view, err := app.GetProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Empty(t, view.Accounts, "nothing observed yet")
	require.True(t, view.ObservedAt.IsZero())

	require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))
	require.EqualValues(t, 1, app.observer.observations.Load())

	repositories, err := app.ListAccessibleRepositories(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	var names []string
	for _, repository := range repositories {
		names = append(names, repository.RepoName)
	}
	require.Equal(t, []string{"acme/api", "acme/shared", "octocat/dotfiles"}, names, "one authorization reaches personal and organization installations")
	view, err = app.GetProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Len(t, view.Accounts, 2)
	require.False(t, view.ObservedAt.IsZero())
	require.EqualValues(t, 1, app.observer.observations.Load(), "reads never observe")

	_, err = app.ListAccessibleRepositories(t.Context(), "", domain.ProviderGitHub)
	require.ErrorIs(t, err, ErrUnauthenticated)
}

// Intent (real): an explicit refresh observes now; a failed observation keeps
// the last observed scope and its time, and reports it as unavailable.
func TestRefreshObservesAndAFailureKeepsTheLastScope(t *testing.T) {
	app := newTestApplication(t)
	app.authorizations.save(mustAuthorization(t, "owner", "octocat"))
	first := time.Date(2026, 10, 13, 8, 0, 0, 0, time.UTC)
	app.observer.now = first
	app.observer.accounts = []domain.ScopeAccount{scopeAccount("2", "acme", githubRepository("github:2", "acme/api"))}
	app.observer.permissions = []domain.ScopePermission{{Name: "contents", Access: "write"}}
	view, err := app.RefreshProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.False(t, view.AccessUnavailable)
	require.Len(t, view.Accounts, 1)
	require.Equal(t, []ProviderPermission{{Name: "contents", Access: "write"}}, view.Permissions)
	require.Equal(t, first, view.ObservedAt)
	require.NotEmpty(t, view.ManageURL)

	app.observer.observeErr = errors.Join(ErrProviderUnavailable, errors.New("github unavailable"))
	view, err = app.RefreshProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err, "a failed observation does not fail the page")
	require.True(t, view.AccessUnavailable)
	require.Len(t, view.Accounts, 1, "the last observed scope stays")
	require.Equal(t, first, view.ObservedAt)
	require.Equal(t, string(domain.ProviderAuthorizationActive), view.State.Status)
}

// Intent (real): losing an installation narrows the reach on the next
// observation without changing the authorization.
func TestRemovedInstallationNarrowsScopeWithoutChangingAuthorization(t *testing.T) {
	app := newTestApplication(t)
	authorization := mustAuthorization(t, "owner", "octocat")
	app.authorizations.save(authorization)
	app.observer.accounts = []domain.ScopeAccount{scopeAccount("2", "acme", githubRepository("github:2", "acme/api"))}
	require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))

	app.observer.accounts = nil
	require.NoError(t, app.ObserveAccessScope(t.Context(), authorization.ID))
	view, err := app.GetProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Empty(t, view.Accounts)
	require.Equal(t, string(domain.ProviderAuthorizationActive), view.State.Status, "losing an installation is not an authorization-level fact")
}

func TestProviderAccessWithoutConnectionShowsRequestedPermissions(t *testing.T) {
	app := newTestApplication(t)
	app.observer.requested = []ProviderPermission{{Name: "contents", Access: "write"}, {Name: "pull_requests", Access: "write"}}
	view, err := app.GetProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Equal(t, "none", view.State.Status)
	require.Empty(t, view.Accounts)
	require.Equal(t, app.observer.requested, view.Permissions)

	view, err = app.RefreshProviderAccess(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Zero(t, app.observer.observations.Load(), "without an Active authorization there is nothing to observe")
	require.Equal(t, "none", view.State.Status)
}

// Intent (real): an installation notification schedules an observation of
// every authorization that reached it and of the acting user's own.
func TestInstallationChangeSchedulesAffectedAuthorizations(t *testing.T) {
	app := newTestApplication(t)
	reached := mustAuthorization(t, "member", "member-login")
	app.authorizations.save(reached)
	sender := mustAuthorization(t, "admin", "admin-login")
	app.authorizations.save(sender)
	unrelated := mustAuthorization(t, "other", "other-login")
	app.authorizations.save(unrelated)
	app.observer.accounts = []domain.ScopeAccount{scopeAccount("42", "acme")}
	require.NoError(t, app.ObserveAccessScope(t.Context(), reached.ID))

	require.NoError(t, app.HandleGitHubInstallationChanged(t.Context(), GitHubInstallationChangedInput{InstallationID: "42", SenderAccountID: sender.ProviderAccountID}))
	require.ElementsMatch(t, []string{reached.ID, sender.ID}, app.observations.scheduled)

	require.ErrorIs(t, app.HandleGitHubInstallationChanged(t.Context(), GitHubInstallationChangedInput{}), domain.ErrInvalidRequiredField)
}

// Intent (real): connecting or reauthorizing observes the reach on the spot;
// when that observation fails, one is scheduled instead.
func TestAuthorizationObservesItsScopeOnTheSpot(t *testing.T) {
	app := newTestApplication(t)
	app.authorizer.authorization = mustAuthorization(t, "owner", "octocat")
	app.observer.accounts = []domain.ScopeAccount{scopeAccount("2", "acme", githubRepository("github:2", "acme/api"))}
	complete := CompleteProviderAuthorizationInput{OwnerID: "owner", Provider: domain.ProviderGitHub, Code: "code", State: "state"}
	info, err := app.CompletePersonalAuthorization(t.Context(), complete)
	require.NoError(t, err)
	repositories, err := app.ListAccessibleRepositories(t.Context(), "owner", domain.ProviderGitHub)
	require.NoError(t, err)
	require.Len(t, repositories, 1)
	require.Empty(t, app.observations.scheduled)

	app.authorizer.authorization = mustAuthorization(t, "owner", "hubot")
	app.observer.observeErr = errors.Join(ErrProviderUnavailable, errors.New("github unavailable"))
	_, err = app.CompletePersonalAuthorization(t.Context(), complete)
	require.NoError(t, err, "a failed observation does not fail the authorization")
	require.Equal(t, []string{info.ID}, app.observations.scheduled)
}

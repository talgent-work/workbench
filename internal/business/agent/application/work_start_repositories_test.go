package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type reachableRepositories struct {
	command.MarketplacePort
	repositories []domain.AccessibleRepository
	err          error
}

func (r reachableRepositories) ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error) {
	return r.repositories, r.err
}

type recentRepositories []string

func (r recentRepositories) ListRecentRepositoryIDs(context.Context, string) ([]string, error) {
	return r, nil
}

func TestRecentRepositoriesAreTheOwnersStillReachableOnesMostRecentFirst(t *testing.T) {
	var reachable []domain.AccessibleRepository
	for _, id := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		reachable = append(reachable, domain.AccessibleRepository{OwnerUserID: "owner", Provider: "github", RepoID: "github:" + id, RepoName: "acme/" + id, DefaultBranch: "main"})
	}
	app := &Application{Catalog: reachableRepositories{repositories: reachable},
		RecentRepositories: recentRepositories{"github:g", "github:lost", "github:a", "github:f", "github:e", "github:d", "github:c"}}
	options, err := app.ListWorkStartRepositories(t.Context(), "owner")
	require.NoError(t, err)
	require.Len(t, options.Repositories, 7)
	require.Equal(t, []string{"github:g", "github:a", "github:f", "github:e", "github:d"}, options.RecentRepositoryIDs,
		"at most five, most recent first, and only repositories the authorization still reaches")
}

func TestRepositoryDiscoveryFailureKeepsStartupPossible(t *testing.T) {
	app := &Application{Catalog: reachableRepositories{err: errors.New("github unavailable")}, RecentRepositories: recentRepositories{"github:a"}}
	options, err := app.ListWorkStartRepositories(t.Context(), "owner")
	require.NoError(t, err)
	require.True(t, options.RepositoriesUnavailable)
	require.Empty(t, options.Repositories)
	require.Empty(t, options.RecentRepositoryIDs)
}

func TestRepositoryDiscoveryNeedsAnOwner(t *testing.T) {
	app := &Application{Catalog: reachableRepositories{}, RecentRepositories: recentRepositories{}}
	_, err := app.ListWorkStartRepositories(t.Context(), " ")
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

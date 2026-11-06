//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

// Existing creation scenarios select this repository reachable under the
// owner's authorization. No grant, repository state or preparation is simulated.
type creationConnections struct{ authorizationcatalogtest.Empty }

func (creationConnections) ListAccessibleRepositories(_ context.Context, owner string) ([]domain.AccessibleRepository, error) {
	return []domain.AccessibleRepository{{OwnerUserID: owner, Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}}, nil
}

func TestWorkRepositorySelectionSurvivesReloadAndReuse(t *testing.T) {
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db),
		creationModels(), &creationCatalog{}, creationConnections{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	id, err := handler.Handle(t.Context(), f.request.Creation)
	require.NoError(t, err)
	work, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	want := domain.WorkRepositorySelection{OwnerUserID: "owner", Repositories: []domain.WorkRepository{{Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main", BaseBranch: "release"}}}
	require.Equal(t, want, work.RepositorySelection)
	require.NoError(t, work.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, f.sessions.Save(t.Context(), work))
	work, err = f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, want, work.RepositorySelection)
	// A repository no longer reachable cannot stop reuse or change the original selection.
	handler = command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, infrastructure.NewWorkAuthorizer(f.db),
		creationModels(), &creationCatalog{}, authorizationcatalogtest.Empty{}, f.sessions, computertest.Checked(startupAuthority{}), infrastructure.NewExecutionRequestsFactory(executionPort{}))
	f.request.Creation.InitialExecution.Selections[0].BaseBranch = "changed"
	reused, err := handler.Handle(t.Context(), f.request.Creation)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	work, err = f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, want, work.RepositorySelection)
	recent, err := infrastructure.NewWorkQueryRepository(f.db).ListRecentRepositoryIDs(t.Context(), "owner")
	require.NoError(t, err)
	require.Equal(t, []string{"123"}, recent, "the owner's own Works supply recently used repositories")
	other, err := infrastructure.NewWorkQueryRepository(f.db).ListRecentRepositoryIDs(t.Context(), "another-owner")
	require.NoError(t, err)
	require.Empty(t, other)
}

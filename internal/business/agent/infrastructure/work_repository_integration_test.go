//go:build integration

package infrastructure_test

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
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
	handler := command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)),
		creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), executionPort{}, creationPreparation(f.db, executionPort{}, creationModels()))
	id, err := handler.Handle(t.Context(), f.request)
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
	handler = command.NewCreateWorkHandler(time.Now, f.agents, f.sessions, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(f.db)),
		creationModels(), commandtest.StartCatalog(&creationCatalog{}, authorizationcatalogtest.Empty{}), computertest.Checked(startupAuthority{}), executionPort{}, creationPreparation(f.db, executionPort{}, creationModels()))
	reuse := f.request
	initial := *f.request.InitialExecution
	initial.RequestID, initial.Input.ID = uuid.NewString(), uuid.NewString()
	reuse.RequestID, reuse.InitialExecution = initial.RequestID, &initial
	reused, err := handler.Handle(t.Context(), reuse)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	// The original request identity cannot be repeated with another selection.
	changed := *f.request.InitialExecution
	changed.Selections = []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "changed"}}
	repeat := f.request
	repeat.InitialExecution = &changed
	_, err = handler.Handle(t.Context(), repeat)
	require.ErrorIs(t, err, command.ErrWorkCreationConflict)
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

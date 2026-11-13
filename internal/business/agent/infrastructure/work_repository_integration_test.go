//go:build integration

package infrastructure_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

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
	return []domain.AccessibleRepository{{OwnerID: owner, Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}}, nil
}

func TestWorkRepositorySelectionSurvivesReloadAndReuse(t *testing.T) {
	f := newWorkCreationFixture(t)
	handler := command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, creationConnections{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
	id, err := handler.Handle(t.Context(), f.request)
	require.NoError(t, err)
	work, err := f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	want := domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/TAL-342", Repositories: []domain.WorkRepository{{Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main", BaseBranch: "release"}}}
	require.Equal(t, want, work.RepositorySelection, "the selection keeps the branch the creating caller named")
	require.Empty(t, work.RepositoryCheckouts)
	reportReady(t, work)
	require.True(t, work.RecordRepositoryCheckouts(domain.ExecutionCheckoutsEnded{WorkID: id, Checkouts: []domain.RepositoryCheckout{{RepositoryID: "123", Reason: domain.CheckoutTimedOut}}}))
	require.NoError(t, f.sessions.Save(t.Context(), work))
	work, err = f.sessions.Get(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, want, work.RepositorySelection)
	require.Equal(t, []domain.RepositoryCheckout{{RepositoryID: "123", Reason: domain.CheckoutTimedOut}}, work.RepositoryCheckouts, "checkout outcomes survive reload")
	require.Empty(t, work.RemainingRepositories())
	// A repository no longer reachable cannot stop reuse or change the original selection.
	handler = command.NewCreateWorkHandler(time.Now, domain.BuiltInPlugins{}, f.agents, f.sessions, access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners), creationModels(), commandtest.StartCatalog(&creationCatalog{}, authorizationcatalogtest.Empty{}), computertest.Checked(startupAuthority{}), creationPreparation(f.db, creationModels()))
	reuse := f.request
	initial := *f.request.InitialExecution
	initial.RequestID, initial.Input.ID = uuid.NewString(), uuid.NewString()
	reuse.RequestID, reuse.InitialExecution = initial.RequestID, &initial
	reused, err := handler.Handle(t.Context(), reuse)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	// A repeat of the original request identity with another selection
	// returns the Work, whose selection stays.
	changed := *f.request.InitialExecution
	changed.Selections = []command.RepositorySelection{{RepositoryID: "123", BaseBranch: "changed"}}
	repeat := f.request
	repeat.InitialExecution = &changed
	repeated, err := handler.Handle(t.Context(), repeat)
	require.NoError(t, err)
	require.Equal(t, id, repeated)
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

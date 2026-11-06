package command_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestPreparationTargetsRemainUniqueAndStableRegardlessOfSelectionOrder(t *testing.T) {
	var repositories []domain.WorkRepository
	for n, name := range []string{"acme/api", "other/api", "acme-api"} {
		repositories = append(repositories, domain.WorkRepository{RepoID: fmt.Sprint(n), Provider: "github", RepoName: name,
			CloneURL: "https://github.com/" + name + ".git", DefaultBranch: "main", BaseBranch: "main"})
	}
	plan := func(repos []domain.WorkRepository) map[string]string {
		r := command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{SubjectID: "owner", WorkID: "work", RequestID: "request", WorkBranch: "talgent/" + "TAL-382"}}
		for _, repo := range repos {
			r.Selections = append(r.Selections, command.RepositorySelection{RepositoryID: repo.RepoID})
		}
		require.NoError(t, r.PlanCheckouts(planningWork(domain.WorkRepositorySelection{OwnerUserID: "owner", Repositories: repos})))
		byRepository, used := map[string]string{}, map[string]bool{}
		for _, checkout := range r.Checkouts {
			require.False(t, used[checkout.TargetPath], "checkout paths must be globally unique")
			used[checkout.TargetPath] = true
			byRepository[checkout.RepoName] = checkout.TargetPath
		}
		return byRepository
	}
	want := plan(repositories)
	require.Equal(t, "/workspace/repos/acme-api", want["acme-api"])
	require.Equal(t, "/workspace/repos/other-api", want["other/api"])
	slices.Reverse(repositories)
	require.Equal(t, want, plan(repositories))
}

func TestPreparationPlansOnlyRetainedWorkRepositories(t *testing.T) {
	selection := domain.WorkRepositorySelection{OwnerUserID: "owner", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "123", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "old-main", BaseBranch: "old-main"},
		{Provider: "github", RepoID: "456", RepoName: "other/api", CloneURL: "https://github.com/other/api.git", DefaultBranch: "main", BaseBranch: "release"},
	}}
	request := command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{SubjectID: "owner", WorkID: "work", RequestID: "request", WorkBranch: "talgent/" + "TAL-382",
		Selections: []command.RepositorySelection{{RepositoryID: "123"}, {RepositoryID: "456", BaseBranch: "release"}}}}
	require.NoError(t, request.PlanCheckouts(planningWork(selection)))
	require.Len(t, request.Checkouts, 2)
	require.Equal(t, "old-main", request.Checkouts[0].BaseBranch)
	require.Equal(t, "release", request.Checkouts[1].BaseBranch)
	require.Equal(t, "/workspace/repos/acme-api", request.Checkouts[0].TargetPath)
	require.Equal(t, "/workspace/repos/other-api", request.Checkouts[1].TargetPath)
	require.Equal(t, "talgent/TAL-382", request.Checkouts[0].WorkBranch)
	require.NoError(t, uuid.Validate(request.Checkouts[0].AccessID))
	original := append([]command.RepositoryCheckout(nil), request.Checkouts...)
	require.NoError(t, request.PlanCheckouts(planningWork(selection)))
	require.Equal(t, original, request.Checkouts, "retry must retain checkout access identities")
	selection.Repositories[0].RepoID = "changed"
	require.ErrorIs(t, request.PlanCheckouts(planningWork(selection)), command.ErrInitialExecutionConflict)
	require.Equal(t, original, request.Checkouts)
}

func planningWork(selection domain.WorkRepositorySelection) *domain.AgentWork {
	return &domain.AgentWork{State: domain.WorkStarting, HarnessProcess: &domain.HarnessProcess{}, Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RepositorySelection: selection}
}

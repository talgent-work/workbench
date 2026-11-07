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

func TestCheckoutTargetsRemainUniqueAndStableRegardlessOfSelectionOrder(t *testing.T) {
	var repositories []domain.WorkRepository
	for n, name := range []string{"acme/api", "other/api", "acme-api"} {
		repositories = append(repositories, domain.WorkRepository{RepoID: fmt.Sprint(n), Provider: "github", RepoName: name,
			CloneURL: "https://github.com/" + name + ".git", DefaultBranch: "main", BaseBranch: "main"})
	}
	plan := func(repos []domain.WorkRepository) map[string]string {
		byRepository, used := map[string]string{}, map[string]bool{}
		for _, checkout := range command.PlanCheckouts("work", domain.WorkRepositorySelection{OwnerUserID: "owner", WorkBranch: "talgent/TAL-382", Repositories: repos}) {
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

func TestCheckoutPlanFollowsTheRetainedSelection(t *testing.T) {
	selection := domain.WorkRepositorySelection{OwnerUserID: "owner", WorkBranch: "talgent/TAL-382", Repositories: []domain.WorkRepository{
		{Provider: "github", RepoID: "123", RepoName: "acme/api", CloneURL: "https://github.com/acme/api.git", DefaultBranch: "old-main", BaseBranch: "old-main"},
		{Provider: "github", RepoID: "456", RepoName: "other/api", CloneURL: "https://github.com/other/api.git", DefaultBranch: "main", BaseBranch: "release"},
	}}

	plan := command.PlanCheckouts("work", selection)

	require.Len(t, plan, 2)
	require.Equal(t, "old-main", plan[0].BaseBranch)
	require.Equal(t, "release", plan[1].BaseBranch)
	require.Equal(t, "/workspace/repos/acme-api", plan[0].TargetPath)
	require.Equal(t, "/workspace/repos/other-api", plan[1].TargetPath)
	require.Equal(t, "talgent/TAL-382", plan[0].WorkBranch)
	require.NoError(t, uuid.Validate(plan[0].AccessID))
	require.NotEqual(t, plan[0].AccessID, plan[1].AccessID)
	require.Equal(t, plan, command.PlanCheckouts("work", selection), "every start plans the same targets and access identities")
	require.NotEqual(t, plan[0].AccessID, command.PlanCheckouts("other-work", selection)[0].AccessID)
	require.Empty(t, command.PlanCheckouts("work", domain.WorkRepositorySelection{}))
}

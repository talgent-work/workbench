package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestRepositoryPreparationRequiresRetainedChoicesAndLiveObligation(t *testing.T) {
	for _, name := range []string{"retained", "explicit branch", "cancelled", "other owner", "missing choice", "changed repository", "changed branch", "no obligation", "closed"} {
		t.Run(name, func(t *testing.T) {
			work := &domain.AgentWork{State: domain.WorkStarting, HarnessProcess: &domain.HarnessProcess{},
				Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project",
				RepositorySelection: domain.WorkRepositorySelection{OwnerUserID: "owner", Repositories: []domain.WorkRepository{{Provider: "github", RepoID: "1", RepoName: "owner/repo", CloneURL: "https://github.com/owner/repo.git", DefaultBranch: "main", BaseBranch: "main"}}}}
			facts := domain.RepositoryPreparation{OwnerUserID: "owner", Choices: []domain.WorkRepositoryChoice{{RepositoryID: "1"}}}
			switch name {
			case "explicit branch":
				facts.Choices[0].BaseBranch = "main"
			case "cancelled":
				facts.Canceled = true
			case "other owner":
				facts.OwnerUserID = "other"
			case "missing choice":
				facts.Choices = nil
			case "changed repository":
				facts.Choices[0].RepositoryID = "other"
			case "changed branch":
				facts.Choices[0].BaseBranch = "release"
			case "no obligation":
				work.HarnessProcess = nil
			case "closed":
				work.State = domain.WorkClosed
			}
			err := work.AdmitRepositoryPreparation(facts)
			if name == "retained" || name == "explicit branch" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, "main", work.RepositorySelection.Repositories[0].BaseBranch)
		})
	}
}

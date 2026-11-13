package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestWorkRetainsRepositoryIdentityAndBranchAcrossExecution(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			fact := repositoryFact()
			input := domain.WorkInput{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(),
				WorkModel:         domain.WorkModelSelection{ConfigurationID: "default"},
				RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(),
				RepositoryInput: domain.WorkRepositoryInput{OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{fact},
					Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
			}
			admit := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}
			work, err := domain.NewAgentWork(input, admit)
			port := follow(work)
			require.NoError(t, err)
			want := domain.WorkRepositorySelection{OwnerID: "owner", WorkBranch: "talgent/work", Repositories: []domain.WorkRepository{{
				Provider: "github", RepoID: "123", RepoName: "example/project", CloneURL: "https://github.com/example/project.git",
				WebURL: "https://github.com/example/project", DefaultBranch: "main", BaseBranch: "main"}}}
			require.Equal(t, want, port.launch().RepositorySelection)
			input.RepositoryInput.Available[0].RepoID = "456"
			port.launch().RepositorySelection.Repositories[0].BaseBranch = "changed"
			ready(t, work)
			if restart {
				exited(t, work, domain.HarnessExit{ExitCode: 1})
			} else {
				require.NoError(t, work.Stop("stop", false))
				exited(t, work, domain.HarnessExit{})
			}
			released(t, work)
			admit.Seat.DeskID = "desk-next"
			if restart {
				require.NoError(t, work.Restart(admit))
			} else {
				require.NoError(t, work.Resume(admit))
			}
			require.Equal(t, want, work.RepositorySelection)
			require.Equal(t, want, port.launch().RepositorySelection)
		})
	}
}

func repositoryFact() domain.AccessibleRepository {
	return domain.AccessibleRepository{OwnerID: "owner", Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}
}

func TestWorkRejectsInvalidRepositorySelectionBeforeExecution(t *testing.T) {
	fact := repositoryFact()
	foreign := fact
	foreign.OwnerID = "other-owner"
	for name, selection := range map[string]domain.WorkRepositoryInput{
		"foreign fact":           {OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{foreign}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"foreign selection":      {OwnerID: "other-owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{foreign}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"unreachable repository": {OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: "unknown"}}},
		"duplicate choice":       {OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}, {RepositoryID: fact.RepoID, BaseBranch: "release"}}},
		"ambiguous repository":   {OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{fact, fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"missing work branch":    {OwnerID: "owner", WorkBranch: " ", Available: []domain.AccessibleRepository{fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"missing identity":       {OwnerID: "owner", WorkBranch: "talgent/work", Available: []domain.AccessibleRepository{{OwnerID: "owner", RepoID: fact.RepoID}}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
	} {
		t.Run(name, func(t *testing.T) {
			id := uuid.NewString()
			work, err := domain.NewAgentWork(domain.WorkInput{WorkCaller: domain.WorkCaller{Kind: "project", ID: "project"}, WorkKey: domain.WorkKey{Kind: "case", ID: "case"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: id,
				WorkModel:         domain.WorkModelSelection{ConfigurationID: "default"},
				RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(), RepositoryInput: selection,
			}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
			port := follow(work)
			require.ErrorIs(t, err, domain.ErrInvalidWork)
			require.Nil(t, work)
			require.Zero(t, port.starts())
		})
	}
}

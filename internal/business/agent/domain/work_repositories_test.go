package domain_test

import (
	"context"
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
			input := domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(),
				WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
				HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, WorkspaceReference: "workspace",
				RepositoryInput: domain.WorkRepositoryInput{OwnerUserID: "owner", Available: []domain.AccessibleRepository{fact},
					Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
			}
			port := &executionPort{}
			admit := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}
			work, err := domain.NewAgentWork(t.Context(), input, admit, port)
			require.NoError(t, err)
			want := domain.WorkRepositorySelection{OwnerUserID: "owner", Repositories: []domain.WorkRepository{{
				Provider: "github", RepoID: "123", RepoName: "example/project", CloneURL: "https://github.com/example/project.git",
				WebURL: "https://github.com/example/project", DefaultBranch: "main", BaseBranch: "main"}}}
			require.Equal(t, want, port.starts[0].RepositorySelection)
			input.RepositoryInput.Available[0].RepoID = "456"
			port.starts[0].RepositorySelection.Repositories[0].BaseBranch = "changed"
			require.NoError(t, work.ConfirmPrepared(context.Background(), time.Now(), nil))
			require.NoError(t, work.ConfirmStarted(context.Background(), time.Now(), nil))
			if restart {
				require.NoError(t, work.RecordExit(t.Context(), domain.HarnessExit{ExitCode: 1}, port))
			} else {
				require.NoError(t, work.Stop(t.Context(), "stop", port))
				require.NoError(t, work.RecordExit(t.Context(), domain.HarnessExit{}, port))
			}
			require.NoError(t, work.ConfirmComputeReclaimed())
			if restart {
				require.NoError(t, work.Restart(t.Context(), admit, port))
			} else {
				require.NoError(t, work.Resume(t.Context(), admit, port))
			}
			require.Equal(t, want, work.RepositorySelection)
			require.Equal(t, want, port.starts[1].RepositorySelection)
		})
	}
}

func repositoryFact() domain.AccessibleRepository {
	return domain.AccessibleRepository{OwnerUserID: "owner", Provider: "github", RepoID: "123",
		RepoName: "example/project", CloneURL: "https://github.com/example/project.git", WebURL: "https://github.com/example/project", DefaultBranch: "main"}
}

func TestWorkRejectsInvalidRepositorySelectionBeforeExecution(t *testing.T) {
	fact := repositoryFact()
	foreign := fact
	foreign.OwnerUserID = "other-owner"
	for name, selection := range map[string]domain.WorkRepositoryInput{
		"foreign fact":           {OwnerUserID: "owner", Available: []domain.AccessibleRepository{foreign}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"foreign selection":      {OwnerUserID: "other-owner", Available: []domain.AccessibleRepository{foreign}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"unreachable repository": {OwnerUserID: "owner", Available: []domain.AccessibleRepository{fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: "unknown"}}},
		"duplicate choice":       {OwnerUserID: "owner", Available: []domain.AccessibleRepository{fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}, {RepositoryID: fact.RepoID, BaseBranch: "release"}}},
		"ambiguous repository":   {OwnerUserID: "owner", Available: []domain.AccessibleRepository{fact, fact}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
		"missing identity":       {OwnerUserID: "owner", Available: []domain.AccessibleRepository{{OwnerUserID: "owner", RepoID: fact.RepoID}}, Choices: []domain.WorkRepositoryChoice{{RepositoryID: fact.RepoID}}},
	} {
		t.Run(name, func(t *testing.T) {
			id := uuid.NewString()
			port := &executionPort{}
			work, err := domain.NewAgentWork(t.Context(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: id,
				WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
				HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, WorkspaceReference: "workspace", RepositoryInput: selection,
			}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, port)
			require.ErrorIs(t, err, domain.ErrInvalidWork)
			require.Nil(t, work)
			require.Empty(t, port.starts)
		})
	}
}

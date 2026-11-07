package domain_test

import (
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestWorkRetainsSelectedAuthorizationAcrossResumeAndRestart(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			githubID, otherID := uuid.NewString(), uuid.NewString()
			input := domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: uuid.NewString(),
				WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
				HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, WorkspaceReference: "workspace",
				AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: "owner",
					Available: []domain.PersonalAuthorization{
						{OwnerUserID: "owner", AuthorizationID: githubID, Provider: "github", Service: "github"},
						{OwnerUserID: "owner", AuthorizationID: otherID, Provider: "other", Service: "other"},
					}},
			}
			admit := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}
			work, err := domain.NewAgentWork(input, admit)
			port := follow(work)
			require.NoError(t, err)
			// Every authorization usable at creation is selected; there is no per-service exclusion.
			want := domain.WorkAuthorizationSelection{OwnerUserID: "owner", Authorizations: []domain.WorkServiceAuthorization{{AuthorizationID: githubID, Provider: "github", Service: "github"}, {AuthorizationID: otherID, Provider: "other", Service: "other"}}}
			require.Equal(t, want, work.AuthorizationSelection)
			require.Equal(t, want, port.launch().AuthorizationSelection)
			// Neither caller-owned facts nor execution adapter snapshots own Work state.
			input.AuthorizationInput.Available[0].AuthorizationID = uuid.NewString()
			port.launch().AuthorizationSelection.Authorizations[0].AuthorizationID = uuid.NewString()
			ready(t, work)
			if restart {
				exited(t, work, domain.HarnessExit{ExitCode: 1})
			} else {
				require.NoError(t, work.Stop("stop", false))
				exited(t, work, domain.HarnessExit{})
			}
			released(t, work)
			if restart {
				require.NoError(t, work.Restart(admit))
			} else {
				require.NoError(t, work.Resume(admit))
			}
			require.Equal(t, want, work.AuthorizationSelection)
			require.Equal(t, want, port.launch().AuthorizationSelection)
		})
	}
}

func TestWorkRejectsAuthorizationFactsForAnotherHuman(t *testing.T) {
	for _, input := range []domain.WorkAuthorizationInput{
		{OwnerUserID: "starter", Available: []domain.PersonalAuthorization{{OwnerUserID: "starter", AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}},
		{OwnerUserID: "owner", Available: []domain.PersonalAuthorization{{OwnerUserID: "another-owner", AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}},
		{Available: []domain.PersonalAuthorization{{OwnerUserID: "owner", AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}},
	} {
		id := uuid.NewString()
		work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
			ID: uuid.NewString(), AgentID: id, WorkspaceReference: "workspace",
			WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
			HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, AuthorizationInput: input,
		}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true})
		port := follow(work)
		require.ErrorIs(t, err, domain.ErrInvalidWork)
		require.Nil(t, work)
		require.Zero(t, port.starts())
	}
}

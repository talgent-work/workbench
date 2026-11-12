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
				WorkModel:         domain.WorkModelSelection{ConfigurationID: "default"},
				RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(),
				AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: "owner",
					Available: []domain.PersonalAuthorization{
						{OwnerUserID: "owner", AuthorizationID: githubID, Provider: "github", Service: "github"},
						{OwnerUserID: "owner", AuthorizationID: otherID, Provider: "other", Service: "other"},
					}},
			}
			admit := domain.StartAdmission{AdmittedAt: time.Now(), AgentID: input.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true}
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
			admit.Seat.DeskID = "desk-next"
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
			ID: uuid.NewString(), AgentID: id,
			WorkModel:         domain.WorkModelSelection{ConfigurationID: "default"},
			RepresentedPerson: "owner", WorkLayout: domain.DefaultWorkLayout(), AuthorizationInput: input,
		}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
		port := follow(work)
		require.ErrorIs(t, err, domain.ErrInvalidWork)
		require.Nil(t, work)
		require.Zero(t, port.starts())
	}
}

// An Application's Work selects the Application's Provider Authorizations
// and repositories as a person's Work selects the person's, and the
// Application's runs are supplied under its own authority (Application stands
// for a person, Personal authorization scope).
func TestApplicationWorkUsesTheApplicationsAuthorizations(t *testing.T) {
	const application = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e"
	githubID, id := uuid.NewString(), uuid.NewString()
	work, err := domain.NewAgentWork(domain.WorkInput{StartedBy: application, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		ID: uuid.NewString(), AgentID: id, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		OwningApplication: application, WorkLayout: domain.DefaultWorkLayout(),
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: application, Available: []domain.PersonalAuthorization{{OwnerUserID: application, AuthorizationID: githubID, Provider: "github", Service: "github"}}},
		RepositoryInput: domain.WorkRepositoryInput{OwnerUserID: application, WorkBranch: "talgent/app", Available: []domain.AccessibleRepository{{OwnerUserID: application, Provider: "github", RepoID: "github:1", RepoName: "acme/app", CloneURL: "https://github.com/acme/app.git", WebURL: "https://github.com/acme/app", DefaultBranch: "main"}},
			Choices: []domain.WorkRepositoryChoice{{RepositoryID: "github:1"}}},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	require.Equal(t, application, work.AuthorizationOwner())
	require.Equal(t, application, work.AuthorizationSelection.OwnerUserID)
	require.Equal(t, application, work.RepositorySelection.OwnerUserID)
	ref, err := work.ServiceAuthorization(application, "github")
	require.NoError(t, err)
	require.Equal(t, githubID, ref.AuthorizationID)
	_, err = work.ServiceAuthorization("0193a7c2-0000-7000-8000-00000000beef", "github")
	require.ErrorIs(t, err, domain.ErrInvalidWork, "nobody but the Application is supplied its credentials")
	require.Equal(t, application, work.LaunchSpecification().AuthorizationOwner())
}

// An Application's Work never takes a person's authorizations, its owner's
// included (No delegation).
func TestApplicationWorkRejectsAPersonsAuthorizations(t *testing.T) {
	const application, owner = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c4e", "0193a7c2-0000-7000-8000-00000000beef"
	id := uuid.NewString()
	_, err := domain.NewAgentWork(domain.WorkInput{StartedBy: application, Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}},
		ID: uuid.NewString(), AgentID: id, WorkModel: domain.WorkModelSelection{ConfigurationID: "default"},
		OwningApplication: application, WorkLayout: domain.DefaultWorkLayout(),
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: owner, Available: []domain.PersonalAuthorization{{OwnerUserID: owner, AuthorizationID: uuid.NewString(), Provider: "github", Service: "github"}}},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: id, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
}

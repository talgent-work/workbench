package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func TestRepositoryAccessUsesRetainedReferenceAndCurrentSupply(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	refs, err := f.app.Plan(t.Context(), "owner", f.work.ID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Empty(t, f.supplied, "planning does not obtain credentials")
	for _, token := range []string{"first-token", "renewed-token"} {
		f.token = token
		resolved, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
		require.NoError(t, err)
		require.Len(t, resolved.Credentials, 1)
		require.Equal(t, refs[0], resolved.Credentials[0].RepositoryAccessReference)
		require.Equal(t, token, resolved.Credentials[0].Password, "each resolution uses the current supply")
		require.Equal(t, application.RepositoryCredentialRequest{OwnerUserID: "owner", AuthorizationID: "b5f00000-0000-4000-8000-000000000382", RepositoryID: "123", CloneURL: "https://github.com/acme/project.git"}, f.supplied[len(f.supplied)-1])
	}
}

func TestRepositoryAccessRefusesUnselectedOrInactiveUseBeforeSupply(t *testing.T) {
	for _, scenario := range []string{"foreign owner", "foreign selection", "other work", "closing", "closed", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRepositoryAccessFixture(t)
			owner, workID := "owner", f.work.ID
			switch scenario {
			case "foreign owner":
				owner = "starter"
			case "foreign selection":
				f.work.RepositorySelection.OwnerUserID = "starter"
			case "other work":
				workID = uuid.NewString()
			case "closing":
				f.work.State = domain.WorkClosing
			case "closed":
				f.work.State = domain.WorkClosed
			case "stopped":
				readyWork(t, f.work)
				require.NoError(t, f.work.Stop("stop", false))
			}
			material, err := f.app.Resolve(t.Context(), owner, workID)
			require.Error(t, err)
			require.Empty(t, material.Credentials)
			require.Empty(t, f.supplied)
		})
	}
}

// A Work entering Closing while its credentials are supplied receives none
// of them (Repository preparation cancellation).
func TestRepositoryAccessDiscardsMaterialWhenTheWorkClosesDuringSupply(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	f.duringSupply = func() { f.work.State = domain.WorkClosing }
	material, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.ErrorIs(t, err, domain.ErrRepositoryPreparationCanceled)
	require.Empty(t, material)
	require.Len(t, f.supplied, 1)
}

// Access follows the Work's retained selection, with the same access
// identity on every start; a selection changed during supply is refused.
func TestRepositoryAccessFollowsTheRetainedSelection(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	first, err := f.app.Plan(t.Context(), "owner", f.work.ID)
	require.NoError(t, err)
	again, err := f.app.Plan(t.Context(), "owner", f.work.ID)
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.Equal(t, query.PlanCheckouts(f.work.ID, f.work.RepositorySelection)[0].AccessID, first[0].AccessID)

	f.duringSupply = func() { f.work.RepositorySelection.Repositories[0].CloneURL = "https://github.com/acme/moved.git" }
	material, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
	require.ErrorIs(t, err, application.ErrRepositoryAccessChanged)
	require.Empty(t, material.Credentials)
}

// A project-scoped Agent's Work lives in its Project's coordination home while the
// person who began it, say a reviewer, made its request.
func TestRepositoryAccessPlansAWorkStartedForAnotherHome(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	agentID := uuid.NewString()
	home := "project/project/project_coordination"
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "review", ID: "review"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID,
		WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, HomeBinding: domain.HomeBinding{OwnerID: home, Namespace: "project_coordination"}, WorkspaceReference: "workspace",
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	f.work = work
	refs, err := f.app.Plan(t.Context(), home, work.ID)
	require.NoError(t, err)
	require.Empty(t, refs)
	_, err = f.app.Plan(t.Context(), "owner", work.ID)
	require.ErrorIs(t, err, access.ErrAccessDenied, "only the Work's home plans its credentials")
}

type repositoryAccessFixture struct {
	application.CredentialSupplyPort
	app          *application.RepositoryAccess
	work         *domain.AgentWork
	supplied     []application.RepositoryCredentialRequest
	token        string
	username     string
	expiresAt    time.Time
	supplyError  error
	duringSupply func()
}

func newRepositoryAccessFixture(t *testing.T, withoutAuthorization ...bool) *repositoryAccessFixture {
	t.Helper()
	agentID := uuid.NewString()
	f := &repositoryAccessFixture{token: "current-token", username: "x-access-token", expiresAt: time.Now().Add(time.Hour)}
	var err error
	f.work, err = domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID,
		WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
		HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, WorkspaceReference: "workspace",
		RepositoryInput: domain.WorkRepositoryInput{OwnerUserID: "owner", WorkBranch: "talgent/TAL-382", Choices: []domain.WorkRepositoryChoice{{RepositoryID: "123"}},
			Available: []domain.AccessibleRepository{{OwnerUserID: "owner", Provider: "github", RepoID: "123", RepoName: "acme/project", CloneURL: "https://github.com/acme/project.git", DefaultBranch: "main"}}},
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: "owner", Available: availableAuthorizations(withoutAuthorization...)},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	f.app = application.NewRepositoryAccess(repositoryWorkStore{f: f}, f)
	return f
}

type repositoryWorkStore struct {
	command.AgentWorkRepository
	f *repositoryAccessFixture
}

func (s repositoryWorkStore) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	if id != s.f.work.ID {
		return nil, domain.ErrWorkNotFound
	}
	return s.f.work, nil
}
func (repositoryWorkStore) Save(context.Context, *domain.AgentWork) error { panic("unexpected save") }

func (f *repositoryAccessFixture) SupplyRepositoryCredential(_ context.Context, r application.RepositoryCredentialRequest) (application.RepositoryCredentialMaterial, error) {
	f.supplied = append(f.supplied, r)
	if f.duringSupply != nil {
		f.duringSupply()
	}
	return application.RepositoryCredentialMaterial{Username: f.username, Password: f.token, ExpiresAt: f.expiresAt}, f.supplyError
}

// readyWork applies its execution's readiness report to the Work's start.
func readyWork(t *testing.T, work *domain.AgentWork) {
	t.Helper()
	_, err := work.ApplyExecutionReport(domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: work.HarnessProcess.StartingSince})
	require.NoError(t, err)
	require.Equal(t, domain.WorkWorking, work.State)
}

func availableAuthorizations(withoutAuthorization ...bool) []domain.PersonalAuthorization {
	if len(withoutAuthorization) > 0 && withoutAuthorization[0] {
		return nil
	}
	return []domain.PersonalAuthorization{{OwnerUserID: "owner", AuthorizationID: "b5f00000-0000-4000-8000-000000000382", Provider: "github", Service: "github"}}
}

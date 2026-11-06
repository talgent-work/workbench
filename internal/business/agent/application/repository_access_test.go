package application_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
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
	for _, scenario := range []string{"foreign owner", "foreign requester", "other work", "unprepared", "canceled", "cleanup", "rejected", "stopped", "missing checkout", "changed repository"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRepositoryAccessFixture(t)
			owner, workID := "owner", f.work.ID
			switch scenario {
			case "foreign owner":
				owner = "starter"
			case "foreign requester":
				f.request.SubjectID = "starter"
			case "other work":
				workID = uuid.NewString()
			case "unprepared":
				f.request.Prepared = false
			case "canceled":
				f.request.PreparationCanceled = true
			case "cleanup":
				f.request.CleanupConfirmed = true
			case "rejected":
				f.request.Rejected = true
			case "stopped":
				require.NoError(t, f.work.ConfirmPrepared(context.Background(), time.Now(), nil))
				require.NoError(t, f.work.ConfirmStarted(context.Background(), time.Now(), nil))
				require.NoError(t, f.work.Stop(t.Context(), "stop", repositoryExecution{}))
			case "missing checkout":
				f.request.Checkouts = nil
			case "changed repository":
				f.request.Checkouts[0].RepoID = "changed"
			}
			material, err := f.app.Resolve(t.Context(), owner, workID)
			require.Error(t, err)
			require.Empty(t, material.Credentials)
			require.Empty(t, f.supplied)
		})
	}
}

func TestRepositoryAccessDiscardsMaterialWhenCanceledDuringSupply(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	f.duringSupply = func() { f.request.PreparationCanceled = true }
	material, err := f.app.Resolve(t.Context(), "owner", f.work.ID)
	require.ErrorIs(t, err, access.ErrAccessDenied)
	require.Empty(t, material)
	require.Len(t, f.supplied, 1)
}

// A project-scoped Agent's Work lives in its Project's coordination home while the
// person who began it, say a reviewer, made its request.
func TestRepositoryAccessPlansAWorkStartedForAnotherHome(t *testing.T) {
	f := newRepositoryAccessFixture(t)
	agentID := uuid.NewString()
	home := "project/project/project_coordination"
	work, err := domain.NewAgentWork(t.Context(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "review", ID: "review"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID,
		WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, HomeBinding: domain.HomeBinding{OwnerID: home, Namespace: "project_coordination"}, WorkspaceReference: "workspace",
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, repositoryExecution{})
	require.NoError(t, err)
	f.work = work
	f.request = command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{SubjectID: "owner", WorkID: work.ID, RequestID: "request"}, Prepared: true}
	refs, err := f.app.Plan(t.Context(), home, work.ID)
	require.NoError(t, err)
	require.Empty(t, refs)
	_, err = f.app.Plan(t.Context(), "owner", work.ID)
	require.ErrorIs(t, err, access.ErrAccessDenied, "only the Work's home plans its credentials")
}

type repositoryAccessFixture struct {
	app          *application.RepositoryAccess
	work         *domain.AgentWork
	request      command.InitialExecutionRequest
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
	f.work, err = domain.NewAgentWork(t.Context(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.NewString(), AgentID: agentID,
		WorkModel:   domain.WorkModelSelection{ConfigurationID: "default"},
		HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "personal"}, WorkspaceReference: "workspace",
		RepositoryInput: domain.WorkRepositoryInput{OwnerUserID: "owner", Choices: []domain.WorkRepositoryChoice{{RepositoryID: "123"}},
			Available: []domain.AccessibleRepository{{OwnerUserID: "owner", Provider: "github", RepoID: "123", RepoName: "acme/project", CloneURL: "https://github.com/acme/project.git", DefaultBranch: "main"}}},
		AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: "owner", Available: availableAuthorizations(withoutAuthorization...)},
	}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, repositoryExecution{})
	require.NoError(t, err)
	f.request = command.InitialExecutionRequest{PrepareWorkExecution: command.PrepareWorkExecution{SubjectID: "owner", WorkID: f.work.ID, RequestID: "request", WorkBranch: "talgent/" + "TAL-382", Selections: []command.RepositorySelection{{RepositoryID: "123"}}}, Prepared: true}
	require.NoError(t, f.request.PlanCheckouts(f.work))
	f.app = application.NewRepositoryAccess(repositoryWorkStore{f}, repositoryRequestStore{f}, f)
	return f
}

type repositoryWorkStore struct{ f *repositoryAccessFixture }

func (s repositoryWorkStore) Get(_ context.Context, id string) (*domain.AgentWork, error) {
	if id != s.f.work.ID {
		return nil, domain.ErrWorkNotFound
	}
	return s.f.work, nil
}
func (repositoryWorkStore) Save(context.Context, *domain.AgentWork) error { panic("unexpected save") }

type repositoryRequestStore struct{ f *repositoryAccessFixture }

func (s repositoryRequestStore) Get(_ context.Context, id string) (command.InitialExecutionRequest, error) {
	if id != s.f.request.WorkID {
		return command.InitialExecutionRequest{}, command.ErrInitialExecutionNotFound
	}
	r := s.f.request
	r.Checkouts = slices.Clone(r.Checkouts)
	return r, nil
}

func (f *repositoryAccessFixture) SupplyRepositoryCredential(_ context.Context, r application.RepositoryCredentialRequest) (application.RepositoryCredentialMaterial, error) {
	f.supplied = append(f.supplied, r)
	if f.duringSupply != nil {
		f.duringSupply()
	}
	return application.RepositoryCredentialMaterial{Username: f.username, Password: f.token, ExpiresAt: f.expiresAt}, f.supplyError
}

type repositoryExecution struct{}

func (repositoryExecution) StartExecution(context.Context, domain.StartExecutionRequest) error {
	return nil
}
func (repositoryExecution) StopExecution(context.Context, string, string) error { return nil }
func (repositoryExecution) ReclaimCompute(context.Context, string) error        { return nil }
func (repositoryExecution) RetireFencedRun(context.Context, string) error       { return nil }

func availableAuthorizations(withoutAuthorization ...bool) []domain.PersonalAuthorization {
	if len(withoutAuthorization) > 0 && withoutAuthorization[0] {
		return nil
	}
	return []domain.PersonalAuthorization{{OwnerUserID: "owner", AuthorizationID: "b5f00000-0000-4000-8000-000000000382", Provider: "github", Service: "github"}}
}

func (repositoryExecution) CancelRunningTurn(context.Context, string) error { return nil }

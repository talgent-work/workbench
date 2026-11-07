package command_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
)

func TestRestartCommitsWorkAndStartRequestBeforeExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	requestID := uuid.Must(uuid.NewV7()).String()
	port := &startupPort{start: func(request domain.StartExecutionRequest) error {
		require.Equal(t, domain.WorkStarting, sessions.session.State)
		require.Contains(t, sessions.requests, requestID, "the start request is committed before execution starts")
		require.Equal(t, sessions.session.ID, request.WorkID)
		return nil
	}}
	admitted, err := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: requestID, SubjectID: "owner", WorkID: sessions.session.ID})
	require.NoError(t, err)
	require.True(t, admitted)
	require.Equal(t, 1, port.calls)
	require.Equal(t, domain.WorkStarting, sessions.session.State, "request acceptance does not establish readiness")
	require.True(t, sessions.session.HoldsProcess())
}

func TestRestartCapacityRejectionDoesNotPersistOrScheduleExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	port := &startupPort{}
	_, err := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{err: domain.ErrCapacityUnavailable}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.Zero(t, sessions.saves)
	require.Empty(t, admittedStarts(sessions))
	require.Zero(t, port.calls)
	require.Equal(t, domain.WorkFailed, sessions.session.State)
}

func TestRestartDeliveryFailureRetainsAdmission(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	failure := errors.New("dispatcher unavailable")
	port := &startupPort{start: func(domain.StartExecutionRequest) error { return failure }}
	admitted, err := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	require.True(t, admitted, "execution recovery owns the undelivered start")
	require.Equal(t, domain.WorkStarting, sessions.session.State)
}

func TestRestartCommitFailureDoesNotStartExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	failure := errors.New("connection lost during save")
	sessions.saveErr = failure
	port := &startupPort{}
	admitted, err := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.ErrorIs(t, err, failure)
	require.False(t, admitted)
	require.Zero(t, port.calls)
}

func TestRepeatedRestartRequestIsIdempotent(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	port := &startupPort{}
	handler := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID}
	for range 2 {
		admitted, err := handler.Handle(context.Background(), request)
		require.NoError(t, err)
		require.True(t, admitted)
	}
	require.Equal(t, 1, port.calls, "a response retry cannot start another process")
	resume := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	err := resume.Handle(context.Background(), request)
	require.ErrorIs(t, err, command.ErrStartRequestConflict, "one request identity cannot select another operation")
}

func TestRestartAuthorizationPrecedesRepositoryAccess(t *testing.T) {
	denied := errors.New("denied")
	_, err := command.NewRestartWorkHandler(time.Now, nil, nil, sessionAccess{err: denied}, computertest.Checked(nil), nil, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String()})
	require.ErrorIs(t, err, denied)
}

func TestResumeAndRestartRetainCapabilitiesAndHarnessSession(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			agents, sessions := startupStores(t)
			session := stoppingSessionFor(t, agents.agent)
			if restart {
				require.NoError(t, session.RecordFailure("failed during stop"))
			}
			require.NoError(t, session.RecordExit(context.Background(), domain.HarnessExit{}, &feedbackPort{}))
			require.NoError(t, session.ConfirmComputeReclaimed())
			sessions.session = session
			harnessSession := session.HarnessSessionReference
			require.NoError(t, agents.agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "changed after session creation"}))
			_, err := agents.agent.ChangeSoul(agents.agent.Owners[0], "Written after the Work started.")
			require.NoError(t, err)
			port := &startupPort{start: func(request domain.StartExecutionRequest) error {
				require.Empty(t, request.Capabilities.Prompt, "existing session retains its original capability snapshot")
				require.Equal(t, "Written after the Work started.", request.Soul, "each start takes up the definition's current Soul")
				require.Equal(t, session.WorkspaceReference, request.WorkspaceReference)
				require.Equal(t, harnessSession, request.HarnessSessionReference)
				if restart {
					require.Equal(t, domain.ContextRecover, request.ContextMode)
				} else {
					require.Equal(t, domain.ContextContinue, request.ContextMode)
				}
				return nil
			}}
			request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: session.ID}
			if restart {
				_, err = command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			} else {
				err = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			}
			require.NoError(t, err)
			require.Equal(t, 1, port.calls)
		})
	}
}

func TestResumeAndRestartRejectAWithdrawnModelConfiguration(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			agents, sessions := startupStores(t)
			session := stoppingSessionFor(t, agents.agent)
			if restart {
				require.NoError(t, session.RecordFailure("failed during stop"))
			}
			require.NoError(t, session.RecordExit(context.Background(), domain.HarnessExit{}, &feedbackPort{}))
			require.NoError(t, session.ConfirmComputeReclaimed())
			sessions.session = session
			state := session.State
			port := &startupPort{}
			// The platform now provides only another configuration.
			provided := query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "glm"}})
			request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: session.ID}
			var err error
			if restart {
				_, err = command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, provided, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			} else {
				err = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, &continuationRequests{}, provided, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			}
			require.ErrorIs(t, err, domain.ErrModelConfigurationWithdrawn)
			require.Equal(t, state, sessions.session.State, "the Work keeps its state")
			require.Zero(t, port.calls, "no execution is requested")
		})
	}
}

func stoppingSessionFor(t *testing.T, agent *domain.AgentDefinition) *domain.AgentWork {
	t.Helper()
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, AgentID: agent.ID, WorkspaceReference: "workspace"}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &feedbackPort{})
	require.NoError(t, err)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, session.Stop(context.Background(), "stop", &feedbackPort{}))
	return session
}

func startupStores(t *testing.T) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	return startupStoresForPurpose(t, "work")
}

func startupStoresForPurpose(t *testing.T, purpose string) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: purpose, UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, AgentID: agent.ID, WorkspaceReference: "workspace"}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &feedbackPort{})
	require.NoError(t, err)
	return &startupAgentStore{agent: agent}, &feedbackSessionStore{session: session}
}

type startupAgentStore struct{ agent *domain.AgentDefinition }

func (s *startupAgentStore) Get(context.Context, string) (*domain.AgentDefinition, error) {
	copy := *s.agent
	return &copy, nil
}
func (s *startupAgentStore) Save(_ context.Context, agent *domain.AgentDefinition) error {
	s.agent = agent
	return nil
}

// capacityCheck is the Work's Computer: it seats every run, or rejects it
// with err. Its starter may use its one Cloud and the others listed.
type capacityCheck struct {
	command.ComputerPort
	err    error
	others []command.UsableComputer
}

func (c capacityCheck) ListUsableComputers(context.Context, string) ([]command.UsableComputer, error) {
	return append([]command.UsableComputer{{ID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Cloud: true}}, c.others...), nil
}

func (c capacityCheck) AdmitRun(context.Context, command.RunAdmission) (command.ComputerAnswer, error) {
	if c.err != nil {
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: c.err}, nil
	}
	return command.ComputerAnswer{Kind: command.ComputerSeated}, nil
}

func (c capacityCheck) LeaveDesk(context.Context, string) error { return nil }

func (c capacityCheck) ReleaseUnusedDesk(context.Context, string) {}

func (c capacityCheck) ListOpenDesks(context.Context, time.Time, string) ([]command.OpenDesk, error) {
	return nil, nil
}

func (c capacityCheck) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

type startupPort struct {
	controlPort
	calls int
	start func(domain.StartExecutionRequest) error
}

func (p *startupPort) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	p.calls++
	if p.start != nil {
		return p.start(request)
	}
	return nil
}

func TestRecoveryResendsAdmittedStartupWithoutNewAdmissionOrContext(t *testing.T) {
	_, sessions := startupStores(t)
	var original domain.StartExecutionRequest
	port := &startupPort{start: func(request domain.StartExecutionRequest) error {
		original = request
		return errors.New("connection lost before delivery")
	}}
	delivery := commandtest.Hold(port)
	_, err := sessions.session.RecoverExecution(context.Background(), delivery)
	require.NoError(t, err)
	err = delivery.Send(context.Background())
	require.Error(t, err)
	port.start = func(request domain.StartExecutionRequest) error {
		require.Equal(t, original, request)
		return nil
	}
	handler := command.NewRecoverWorkHandler(time.Now, sessions, port, &commandtest.InputBuffer{}, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil, nil)
	require.NoError(t, handler.Handle(context.Background(), sessions.session.ID))
	require.Equal(t, 2, port.calls)
	require.Empty(t, admittedStarts(sessions), "recovery must not admit a new request")
	require.Equal(t, domain.WorkStarting, sessions.session.State)
}

// Every run continues the same Harness Session.
func TestResumeAfterFirstStartContinuesTheSameConversation(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	sessions.session.CapabilitySnapshot.Prompt = "Retained instructions"
	originalID, harnessSession := sessions.session.ID, sessions.session.HarnessSessionReference
	var launches []domain.StartExecutionRequest
	port := &startupPort{start: func(request domain.StartExecutionRequest) error { launches = append(launches, request); return nil }}
	handler := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	first := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: originalID}
	sessions.requests = map[string]query.StartRequest{first.RequestID: {ID: first.RequestID, WorkID: originalID, Kind: query.StartKindStart}}
	require.NoError(t, command.NewRecoverWorkHandler(time.Now, sessions, port, &commandtest.InputBuffer{}, command.NewInputAcceptance(sessions, &commandtest.InputBuffer{}), &commandtest.InputSettlements{}, nil, nil).Handle(ctx, originalID))
	require.Equal(t, domain.ContextFresh, launches[0].ContextMode)
	require.ErrorIs(t, handler.Handle(ctx, first), command.ErrStartRequestConflict, "Resume never adopts the first start's request")
	require.Len(t, launches, 1, "a first start is admitted by creation, never by Resume")
	require.NoError(t, sessions.session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.session.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, sessions.session.Stop(ctx, "normal-stop", &feedbackPort{}))
	require.NoError(t, sessions.session.RecordExit(ctx, domain.HarnessExit{}, &feedbackPort{}))
	require.NoError(t, sessions.session.ConfirmComputeReclaimed())
	require.NoError(t, agents.agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "New definition instructions"}))
	second := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: originalID}
	require.NoError(t, handler.Handle(ctx, second))
	require.Equal(t, query.StartKindResume, sessions.requests[second.RequestID].Kind)
	require.Len(t, launches, 2)
	require.Equal(t, domain.ContextContinue, launches[1].ContextMode)
	require.Equal(t, harnessSession, launches[0].HarnessSessionReference)
	require.Equal(t, harnessSession, launches[1].HarnessSessionReference)
	require.Equal(t, "Retained instructions", launches[1].Capabilities.Prompt)
	require.Equal(t, launches[0].WorkspaceReference, launches[1].WorkspaceReference)
	require.Equal(t, launches[0].HomeBinding, launches[1].HomeBinding)
	require.NoError(t, handler.Handle(ctx, second))
	require.Len(t, launches, 2)
}

func TestResumeDoesNotRestartFailedOrClosedConversations(t *testing.T) {
	for _, state := range []domain.WorkState{domain.WorkFailed, domain.WorkClosing, domain.WorkClosed, domain.WorkStarting, domain.WorkWorking, domain.WorkStopping} {
		t.Run(string(state), func(t *testing.T) {
			agents, sessions := startupStores(t)
			sessions.session.State = state
			harnessSession := sessions.session.HarnessSessionReference
			port := &startupPort{}
			handler := command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, port, &continuationRequests{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
			err := handler.Handle(context.Background(), command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID})
			require.ErrorIs(t, err, domain.ErrWorkTransition)
			require.Zero(t, port.calls)
			require.Zero(t, sessions.saves)
			require.Empty(t, admittedStarts(sessions))
			require.Equal(t, harnessSession, sessions.session.HarnessSessionReference)
		})
	}
}

func failedStartupStores(t *testing.T) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	agents, sessions := startupStores(t)
	require.NoError(t, sessions.session.RecordExit(t.Context(), domain.HarnessExit{ExitCode: 1, Reason: "process failed to start"}, &feedbackPort{}))
	require.NoError(t, sessions.session.ConfirmComputeReclaimed())
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.False(t, sessions.session.HoldsProcess())
	return agents, sessions
}

// admittedStarts are the start requests the store committed with a start.
func admittedStarts(sessions *feedbackSessionStore) map[string]query.StartRequest {
	sessions.mu.Lock()
	defer sessions.mu.Unlock()
	admitted := map[string]query.StartRequest{}
	for id, record := range sessions.requests {
		if record.Admitted() {
			admitted[id] = record
		}
	}
	return admitted
}

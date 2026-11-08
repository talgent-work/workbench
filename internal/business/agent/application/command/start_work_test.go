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

func restartHandler(sessions *feedbackSessionStore, agents *startupAgentStore, computers command.ComputerPort) *command.RestartWorkHandler {
	return command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, computers, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
}

func resumeHandler(sessions *feedbackSessionStore, agents *startupAgentStore, computers command.ComputerPort) *command.ResumeWorkHandler {
	return command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, computers, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
}

// A Restart commits the Work with its start, on the Desk its request
// identity derives; the committed Work holds the admitted start its execution
// begins from, and nothing begins before.
func TestRestartCommitsWorkAndStartRequestBeforeExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	requestID := uuid.Must(uuid.NewV7()).String()
	admitted, err := restartHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), command.StartWork{RequestID: requestID, SubjectID: "owner", WorkID: sessions.session.ID})
	require.NoError(t, err)
	require.True(t, admitted)
	starts := sessions.admittedStarts()
	require.Len(t, starts, 1)
	require.Equal(t, sessions.session.ID, starts[0].WorkID)
	require.Equal(t, sessions.session.HarnessProcess.StartingSince, starts[0].StartAdmittedAt)
	require.Equal(t, domain.WorkStarting, sessions.session.State, "request acceptance does not establish readiness")
	require.True(t, sessions.session.HoldsProcess())
	w := worldOver(t, sessions)
	execution := w.begin(t)
	require.True(t, execution.Serves(starts[0].StartAdmittedAt), "the execution begins from the committed start")
	require.Equal(t, sessions.session.ID, execution.Launch.WorkID)
}

func TestRestartCapacityRejectionDoesNotPersistOrScheduleExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	_, err := restartHandler(sessions, agents, capacityCheck{err: domain.ErrCapacityUnavailable}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.ErrorIs(t, err, domain.ErrCapacityUnavailable)
	require.Zero(t, sessions.saves)
	require.Empty(t, sessions.admittedStarts())
	require.Equal(t, domain.WorkFailed, sessions.session.State)
}

// An admitted start whose execution cannot begin now stays admitted; the
// Work's recovery begins it.
// A refused Resume answers its caller and retains nothing: no save, beyond
// the Reach Resume Due mark of a Resume refused as unreachable.
func TestResumeRefusalRetainsNothingButReachResumeDue(t *testing.T) {
	for name, refusal := range map[string]error{"capacity": domain.ErrCapacityUnavailable, "unreachable": domain.ErrComputerUnreachable} {
		t.Run(name, func(t *testing.T) {
			agents, sessions := startupStores(t)
			sessions.session.State = domain.WorkStopped
			sessions.session.HarnessProcess.Exited, sessions.session.HarnessProcess.ComputeReclaimed = true, true
			err := resumeHandler(sessions, agents, capacityCheck{err: refusal}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
			require.ErrorIs(t, err, refusal)
			require.Empty(t, sessions.admittedStarts())
			require.Equal(t, domain.WorkStopped, sessions.session.State)
			require.Equal(t, name == "unreachable", sessions.session.ReachResumeDue)
			if name == "capacity" {
				require.Zero(t, sessions.saves)
			}
		})
	}
}

func TestRestartRetainsAdmissionWhileItsExecutionCannotBegin(t *testing.T) {
	ctx := context.Background()
	agents, sessions := failedStartupStores(t)
	admitted, err := restartHandler(sessions, agents, capacityCheck{}).Handle(ctx, command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.NoError(t, err)
	require.True(t, admitted)
	w := worldOver(t, sessions)
	failure := errors.New("database unavailable")
	w.executions.SaveErr = failure
	require.ErrorIs(t, w.coordination.BeginExecution(ctx, sessions.session.ID), failure)
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	require.Contains(t, executionRequests(sessions.session), domain.EventKindWorkStartAdmitted, "the start request is retained")
	w.executions.SaveErr = nil
	require.NoError(t, w.coordination.Coordinate(ctx, sessions.session.ID))
	require.Equal(t, 1, w.executions.Count())
}

func TestRestartCommitFailureDoesNotStartExecution(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	failure := errors.New("connection lost during save")
	sessions.saveErr = failure
	admitted, err := restartHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.ErrorIs(t, err, failure)
	require.False(t, admitted)
	require.Empty(t, sessions.admittedStarts())
}

func TestRepeatedRestartRequestIsIdempotent(t *testing.T) {
	agents, sessions := failedStartupStores(t)
	handler := restartHandler(sessions, agents, capacityCheck{})
	request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: sessions.session.ID}
	for range 2 {
		admitted, err := handler.Handle(context.Background(), request)
		require.NoError(t, err)
		require.True(t, admitted)
	}
	require.NoError(t, resumeHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), request), "the identity's Desk is held by the start it admitted")
	require.Len(t, sessions.admittedStarts(), 1, "a response retry cannot start another process")
}

func TestRestartAuthorizationPrecedesRepositoryAccess(t *testing.T) {
	denied := errors.New("denied")
	_, err := command.NewRestartWorkHandler(time.Now, nil, nil, sessionAccess{err: denied}, computertest.Checked(nil), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(context.Background(), command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String()})
	require.ErrorIs(t, err, denied)
}

func TestResumeAndRestartRetainCapabilitiesAndHarnessSession(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			agents, sessions := startupStores(t)
			session := endedSessionFor(t, agents.agent, restart)
			sessions.session = session
			harnessSession := session.HarnessSessionReference
			require.NoError(t, agents.agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "changed after session creation"}))
			_, err := agents.agent.ChangeSoul(agents.agent.Owners[0], "Written after the Work started.")
			require.NoError(t, err)
			request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: session.ID}
			if restart {
				_, err = restartHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), request)
			} else {
				err = resumeHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), request)
			}
			require.NoError(t, err)
			require.Len(t, sessions.admittedStarts(), 1)
			launch := sessions.session.LaunchSpecification()
			require.Empty(t, launch.Capabilities.Prompt, "existing session retains its original capability snapshot")
			require.Equal(t, "Written after the Work started.", launch.Soul, "each start takes up the definition's current Soul")
			require.Equal(t, session.LocatedLayout, launch.LocatedLayout)
			require.Equal(t, harnessSession, launch.HarnessSessionReference)
			if restart {
				require.Equal(t, domain.ContextRecover, launch.ContextMode)
			} else {
				require.Equal(t, domain.ContextContinue, launch.ContextMode)
			}
		})
	}
}

func TestResumeAndRestartRejectAWithdrawnModelConfiguration(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "resume", true: "restart"}[restart], func(t *testing.T) {
			agents, sessions := startupStores(t)
			session := endedSessionFor(t, agents.agent, restart)
			sessions.session = session
			state := session.State
			// The platform now provides only another configuration.
			provided := query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "glm"}})
			request := command.StartWork{RequestID: uuid.Must(uuid.NewV7()).String(), SubjectID: "owner", WorkID: session.ID}
			var err error
			if restart {
				_, err = command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, provided, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			} else {
				err = command.NewResumeWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, provided, commandtest.UnchangedPlugins{}).Handle(context.Background(), request)
			}
			require.ErrorIs(t, err, domain.ErrModelConfigurationWithdrawn)
			require.Equal(t, state, sessions.session.State, "the Work keeps its state")
			require.Empty(t, sessions.admittedStarts(), "no execution is requested")
		})
	}
}

// stoppingSessionFor is a running Work of the definition whose stop is
// admitted.
func stoppingSessionFor(t *testing.T, agent *domain.AgentDefinition) *domain.AgentWork {
	t.Helper()
	session, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(), AgentID: agent.ID}, fixtureAdmission(agent.ID))
	require.NoError(t, err)
	report(t, session, readyReport)
	require.NoError(t, session.Stop("stop", false))
	return settled(session)
}

// endedSessionFor is a Work of the definition whose run exited and was
// released after its stop: Stopped, or Failed when failed is asked.
func endedSessionFor(t *testing.T, agent *domain.AgentDefinition, failed bool) *domain.AgentWork {
	t.Helper()
	session := stoppingSessionFor(t, agent)
	if failed {
		require.NoError(t, session.RecordFailure("failed during stop"))
	}
	report(t, session, exitedReport)
	report(t, session, releasedReport)
	return settled(session)
}

func startupStores(t *testing.T) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	return startupStoresForPurpose(t, "work")
}

func startupStoresForPurpose(t *testing.T, purpose string) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: purpose, UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	session, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), RepresentedPerson: "user-home", WorkLayout: domain.DefaultWorkLayout(), AgentID: agent.ID}, fixtureAdmission(agent.ID))
	require.NoError(t, err)
	return &startupAgentStore{agent: agent}, &feedbackSessionStore{session: settled(session)}
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

func (c capacityCheck) AdmitRun(ctx context.Context, run command.RunAdmission) (command.ComputerAnswer, error) {
	if c.err != nil {
		return command.ComputerAnswer{Kind: command.ComputerRejected, Rejection: c.err}, nil
	}
	return command.ComputerAnswer{Kind: command.ComputerSeated, Computer: c.kindOf(ctx, run.ComputerID)}, nil
}

// kindOf is what a listed Computer is; any Computer not listed as a Cloud is
// Managed.
func (c capacityCheck) kindOf(ctx context.Context, computerID string) domain.ComputerKind {
	listed, _ := c.ListUsableComputers(ctx, "")
	for _, computer := range listed {
		if computer.ID == computerID && computer.Cloud {
			return domain.ComputerCloud
		}
	}
	return domain.ComputerManaged
}

func (c capacityCheck) LeaveDesk(context.Context, string) error { return nil }

func (c capacityCheck) ReleaseUnusedDesk(context.Context, string) {}

func (c capacityCheck) ListOpenDesks(context.Context, time.Time, string) ([]command.OpenDesk, error) {
	return nil, nil
}

func (c capacityCheck) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

// A start whose execution's beginning was lost is begun by the Work's
// recovery with the launch the start fixed, without a new admission; a
// repeated recovery begins no second execution.
func TestRecoveryBeginsAnAdmittedStartWithoutNewAdmissionOrContext(t *testing.T) {
	ctx := context.Background()
	_, sessions := startupStores(t)
	original := sessions.session.LaunchSpecification()
	w := worldOver(t, sessions)
	for range 2 {
		require.NoError(t, w.coordination.Coordinate(ctx, sessions.session.ID))
	}
	require.Equal(t, 1, w.executions.Count())
	launch := w.execution(t).Launch
	launch.ComputerName, launch.PreparationBound = "", time.Time{}
	require.Equal(t, original, launch)
	require.Empty(t, sessions.admittedStarts(), "recovery must not admit a new request")
	require.Equal(t, domain.WorkStarting, sessions.session.State)
}

// Every run continues the same Harness Session.
func TestResumeAfterFirstStartContinuesTheSameConversation(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	sessions.session.CapabilitySnapshot.Prompt = "Retained instructions"
	originalID, harnessSession := sessions.session.ID, sessions.session.HarnessSessionReference
	launches := []domain.LaunchSpecification{sessions.session.LaunchSpecification()}
	handler := resumeHandler(sessions, agents, capacityCheck{})
	first := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: originalID}
	require.Equal(t, domain.ContextFresh, launches[0].ContextMode)
	require.ErrorIs(t, handler.Handle(ctx, first), domain.ErrWorkTransition, "Resume never adopts the first start")
	require.Empty(t, sessions.admittedStarts(), "a first start is admitted by creation, never by Resume")
	report(t, sessions.session, readyReport)
	require.NoError(t, sessions.session.Stop("normal-stop", false))
	report(t, sessions.session, exitedReport)
	report(t, sessions.session, releasedReport)
	settled(sessions.session)
	require.NoError(t, agents.agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "New definition instructions"}))
	second := command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: originalID}
	require.NoError(t, handler.Handle(ctx, second))
	require.Len(t, sessions.admittedStarts(), 1)
	launches = append(launches, sessions.session.LaunchSpecification())
	require.Equal(t, domain.ContextContinue, launches[1].ContextMode)
	require.Equal(t, harnessSession, launches[0].HarnessSessionReference)
	require.Equal(t, harnessSession, launches[1].HarnessSessionReference)
	require.Equal(t, "Retained instructions", launches[1].Capabilities.Prompt)
	require.Equal(t, launches[0].LocatedLayout, launches[1].LocatedLayout)
	require.Equal(t, launches[0].AgentHarness, launches[1].AgentHarness)
	require.NoError(t, handler.Handle(ctx, second))
	require.Len(t, sessions.admittedStarts(), 1)
}

func TestResumeDoesNotRestartFailedOrClosedConversations(t *testing.T) {
	for _, state := range []domain.WorkState{domain.WorkFailed, domain.WorkClosing, domain.WorkClosed, domain.WorkStarting, domain.WorkWorking, domain.WorkStopping} {
		t.Run(string(state), func(t *testing.T) {
			agents, sessions := startupStores(t)
			sessions.session.State = state
			harnessSession := sessions.session.HarnessSessionReference
			err := resumeHandler(sessions, agents, capacityCheck{}).Handle(context.Background(), command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID})
			require.ErrorIs(t, err, domain.ErrWorkTransition)
			require.Empty(t, sessions.admittedStarts())
			require.Zero(t, sessions.saves)
			require.Equal(t, harnessSession, sessions.session.HarnessSessionReference)
		})
	}
}

// failedStartupStores hold a Work whose first process exited before it became
// ready and whose compute was released.
func failedStartupStores(t *testing.T) (*startupAgentStore, *feedbackSessionStore) {
	t.Helper()
	agents, sessions := startupStores(t)
	report(t, sessions.session, func(workID string, start time.Time) domain.Event {
		return domain.ExecutionExited{WorkID: workID, StartAdmittedAt: start, Exit: domain.HarnessExit{ExitCode: 1, Reason: "process failed to start"}}
	})
	report(t, sessions.session, releasedReport)
	settled(sessions.session)
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.False(t, sessions.session.HoldsProcess())
	return agents, sessions
}

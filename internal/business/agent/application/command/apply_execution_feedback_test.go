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
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func feedbackHandler(store *feedbackSessionStore, processes command.ProcessObservationPort, port domain.ExecutionPort) *command.ApplyExecutionFeedbackHandler {
	return command.NewApplyExecutionFeedbackHandler(time.Now, store, processes, port, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
}

// Intent (real): the exit fact of the current process is acknowledged on its
// allocation and applied; the Work stops only after compute release.
func TestExitFeedbackStopsOnlyAfterComputeRelease(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	port := &feedbackPort{}
	processes := exitedProcess()
	handler := feedbackHandler(store, processes, port)
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: session.ID, ProcessID: "process", ExitCode: 143}))
	require.Equal(t, []string{"process"}, processes.acknowledged)
	require.Equal(t, domain.WorkStopping, store.session.State, "an exit after readiness with an admitted stop is normal whatever its code")
	require.Empty(t, store.session.FailureReason)
	require.False(t, store.session.HoldsProcess(), "the exited process no longer counts toward concurrency")
	require.Equal(t, 1, port.reclaims)
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: session.ID, ProcessID: "process", ExitCode: 143}))
	require.Equal(t, 1, port.reclaims, "a redelivered exit fact changes nothing")
	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: session.ID}))
	require.Equal(t, domain.WorkStopped, store.session.State)
}

// Intent (real): an exit fact naming another process, such as an earlier run's
// late one, changes nothing, nor does feedback about another process.
func TestFeedbackAboutAnotherProcessChangesNothing(t *testing.T) {
	session := runningSession(t)
	store := &feedbackSessionStore{session: session}
	port := &feedbackPort{}
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}
	handler := feedbackHandler(store, processes, port)
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: session.ID, ProcessID: "earlier", ExitCode: 1}))
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.TurnStarted, WorkID: session.ID, ProcessID: "earlier", ObservationSequence: 1_000_000}))
	require.Equal(t, domain.WorkWorking, store.session.State)
	require.True(t, store.session.HoldsProcess())
	require.Zero(t, port.reclaims)
	require.Empty(t, processes.acknowledged)
	require.Zero(t, store.session.HarnessProcess.Turn.Sequence)
	require.ErrorIs(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.TurnStarted, WorkID: session.ID, ObservationSequence: 1}), domain.ErrInvalidWork, "Dispatcher feedback names its process")
}

// Intent (real): an exit without an admitted stop is a failure that keeps its
// exit code; Dispatcher sends no reason for an exit it merely observed.
func TestUnrequestedExitRecordsFailureWithItsCode(t *testing.T) {
	session := runningSession(t)
	store := &feedbackSessionStore{session: session}
	port := &feedbackPort{}
	require.NoError(t, feedbackHandler(store, exitedProcess(), port).Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: session.ID, ProcessID: "process", ExitCode: 1}))
	require.Equal(t, domain.WorkFailed, store.session.State)
	require.Equal(t, "native_process_exited_unexpectedly (exit code 1)", store.session.FailureReason)
	require.Equal(t, 1, port.reclaims)
}

func TestComputeReleaseBeforeExitDoesNotReleaseCurrentProcess(t *testing.T) {
	session := runningSession(t)
	store := &feedbackSessionStore{session: session}
	require.NoError(t, feedbackHandler(store, &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning}}, &feedbackPort{}).Handle(context.Background(), command.ExecutionFeedback{Kind: command.ComputeReclaimed, WorkID: session.ID}))
	require.False(t, store.session.HarnessProcess.ComputeReclaimed)
	require.Equal(t, domain.WorkWorking, store.session.State)
}

func TestContradictoryTurnFeedbackCannotPersist(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	err := feedbackHandler(store, exitedProcess(), &feedbackPort{}).Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process",
		Kind: command.TurnStarted, WorkID: session.ID, ObservationSequence: 1, TurnCompleted: true,
	})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	require.Zero(t, store.saves)
}

func TestFailedReclamationRequestStillPersistsObservedExit(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	failure := errors.New("provider temporarily unavailable")
	err := feedbackHandler(store, exitedProcess(), &feedbackPort{reclaimErr: failure}).Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: session.ID})
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, domain.ErrExecutionRequest)
	require.Equal(t, 1, store.saves)
	require.True(t, store.session.HarnessProcess.Exited)
	require.Equal(t, domain.WorkStopping, store.session.State)
}

func TestExitMustBeSavedBeforeComputeCanBeReclaimed(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed", true: "save-failed"}[failSave], func(t *testing.T) {
			session := stoppingSession(t)
			store := &feedbackSessionStore{session: session}
			failure := errors.New("database unavailable")
			if failSave {
				store.saveErr = failure
			}
			reclaimed := false
			port := observingReclaimPort{onReclaim: func() {
				reclaimed = true
				require.True(t, store.session.HarnessProcess.Exited)
				require.Equal(t, 1, store.saves)
			}}
			err := feedbackHandler(store, exitedProcess(), port).Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: session.ID})
			if failSave {
				require.ErrorIs(t, err, failure)
				require.False(t, reclaimed)
			} else {
				require.NoError(t, err)
				require.True(t, reclaimed)
			}
		})
	}
}

func TestAuthoritativeEnvironmentLossFailsStoppingWork(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	port := &feedbackPort{}
	// Observing a live allocation asks Dispatcher, which no longer reaches a
	// lost sandbox; the loss must not depend on it.
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessPending}}
	require.NoError(t, feedbackHandler(store, processes, port).Handle(context.Background(), command.ExecutionFeedback{Kind: command.EnvironmentLost, WorkID: session.ID}))
	require.Zero(t, processes.calls, "an authoritative loss is the observation")
	require.Equal(t, domain.WorkFailed, store.session.State, "missing normal exit cannot become Stopped")
	require.NotEmpty(t, store.session.FailureReason)
	require.True(t, store.session.HarnessProcess.ComputeReclaimed)
	require.False(t, store.session.HarnessProcess.Exited, "environment loss does not manufacture a normal exit")
	require.Zero(t, port.reclaims, "confirmed absence needs no new delete request")
	require.NoError(t, store.session.Close(context.Background(), "close-after-loss", domain.ClosureFacts{}, port))
	require.Equal(t, domain.WorkClosed, store.session.State)
}

func TestAuthoritativeEnvironmentLossFailsWorkingWork(t *testing.T) {
	session := runningSession(t)
	store := &feedbackSessionStore{session: session}
	processes := &observedProcess{observation: domain.ProcessObservation{State: domain.ProcessRunning, Ready: true}}
	require.NoError(t, feedbackHandler(store, processes, &feedbackPort{}).Handle(context.Background(), command.ExecutionFeedback{Kind: command.EnvironmentLost, WorkID: session.ID}))
	require.Zero(t, processes.calls)
	require.Equal(t, domain.WorkFailed, store.session.State)
	require.Equal(t, "execution environment lost", store.session.FailureReason)
	require.True(t, store.session.HarnessProcess.EnvironmentLost)
}

func TestEnvironmentLossAfterNormalExitKeepsTheStop(t *testing.T) {
	session := stoppingSession(t)
	store := &feedbackSessionStore{session: session}
	processes := exitedProcess()
	handler := feedbackHandler(store, processes, &feedbackPort{})
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: session.ID}))
	require.NoError(t, handler.Handle(context.Background(), command.ExecutionFeedback{Kind: command.EnvironmentLost, WorkID: session.ID}))
	require.Equal(t, domain.WorkStopped, store.session.State)
	require.Empty(t, store.session.FailureReason)
}

func definitionFixture(t *testing.T) *domain.AgentDefinition {
	t.Helper()
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{}})
	require.NoError(t, err)
	return agent
}

func runningSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	agent := definitionFixture(t)
	session, err := domain.NewAgentWork(context.Background(), domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "owner", Capabilities: domain.CapabilitySnapshot{Connectors: []domain.SelectedConnector{connectorcatalogtest.Required().SelectedConnector}}, ID: uuid.Must(uuid.NewV7()).String(), HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, AgentID: agent.ID, WorkspaceReference: "workspace"}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: agent.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &feedbackPort{})
	require.NoError(t, err)
	require.NoError(t, session.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, session.ConfirmStarted(context.Background(), time.Now(), nil))
	return session
}

func stoppingSession(t *testing.T) *domain.AgentWork {
	t.Helper()
	session := runningSession(t)
	require.NoError(t, session.Stop(context.Background(), "stop", &feedbackPort{}))
	return session
}

type feedbackPort struct {
	reclaims   int
	retires    int
	reclaimErr error
}

func (*feedbackPort) StartExecution(context.Context, domain.StartExecutionRequest) error { return nil }
func (*feedbackPort) StopExecution(context.Context, string, string) error                { return nil }
func (p *feedbackPort) ReclaimCompute(context.Context, string) error {
	p.reclaims++
	return p.reclaimErr
}

func (p *feedbackPort) RetireFencedRun(context.Context, string) error {
	p.retires++
	return nil
}

type observingReclaimPort struct {
	controlPort
	onReclaim func()
}

func (p observingReclaimPort) ReclaimCompute(context.Context, string) error {
	p.onReclaim()
	return nil
}

// Intent (real): a resumed process that becomes ready with nothing admitted
// is idle whichever readiness fact arrives last; admitted input keeps it busy.
func TestReadyProcessIsIdleOnlyWhenNothingIsComing(t *testing.T) {
	ctx := context.Background()
	resumed := func(t *testing.T) *domain.AgentWork {
		session := stoppingSession(t)
		require.NoError(t, session.RecordExit(ctx, domain.HarnessExit{}, &feedbackPort{}))
		require.NoError(t, session.ConfirmComputeReclaimed())
		require.NoError(t, session.Resume(ctx, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: session.AgentID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, &feedbackPort{}))
		return session
	}
	for _, tc := range []struct {
		name     string
		buffered []command.ExecutionInput
		order    []command.ExecutionFeedbackKind
		idle     bool
	}{
		{"nothing coming", nil, []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted}, true},
		{"nothing coming, started reported first", nil, []command.ExecutionFeedbackKind{command.AgentStarted, command.ExecutorPrepared}, true},
		{"buffered input", []command.ExecutionInput{{ID: uuid.NewString(), Text: "queued for the next process"}}, []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &feedbackSessionStore{session: resumed(t)}
			handler := command.NewApplyExecutionFeedbackHandler(time.Now, store, exitedProcess(), &feedbackPort{}, &commandtest.InputBuffer{Inputs: tc.buffered}, &commandtest.InputSettlements{})
			for _, kind := range tc.order {
				require.NoError(t, handler.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: store.session.ID}))
			}
			require.Equal(t, domain.WorkWorking, store.session.State)
			require.Equal(t, tc.idle, store.session.Idle())
		})
	}
}

func (*feedbackPort) CancelRunningTurn(context.Context, string) error { return nil }

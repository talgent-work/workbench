package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// runningWorld is a Work whose launched "process" became ready.
func runningWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, startingSession(t))
	w.ready(t)
	return w
}

// stoppingWorld is a running Work whose stop reached its execution.
func stoppingWorld(t *testing.T) *world {
	t.Helper()
	w := runningWorld(t)
	w.stop(t, "stop")
	return w
}

// The exit fact of the current process is recorded on its execution and
// applied to the Work, which asks for the execution's retirement; the Work
// stops only once the execution's release is confirmed.
func TestExitFeedbackStopsOnlyAfterComputeRelease(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	exit := command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: "process", ExitCode: 143}
	require.NoError(t, w.feedback().Handle(ctx, exit))
	require.True(t, w.execution(t).Process.Exited)
	require.Equal(t, domain.WorkStopping, w.work().State, "an exit after readiness with an admitted stop is normal whatever its code")
	require.Empty(t, w.work().FailureReason)
	require.False(t, w.work().HoldsProcess(), "the exited process no longer counts toward concurrency")
	require.Contains(t, executionRequests(w.work()), domain.EventKindWorkComputeReleaseRequested)
	require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
	require.Equal(t, domain.RetiredByExit, w.execution(t).Retirement)
	saves := w.executions.Saves
	require.NoError(t, w.feedback().Handle(ctx, exit))
	require.Equal(t, saves, w.executions.Saves, "a redelivered exit fact changes nothing")
	require.Equal(t, domain.WorkStopping, w.work().State)
	w.release(t)
	require.Equal(t, domain.WorkStopped, w.work().State)
}

// An exit fact naming another process, such as an earlier run's late one,
// changes nothing, nor does feedback about another process.
func TestFeedbackAboutAnotherProcessChangesNothing(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: "earlier", ExitCode: 1}))
	require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{Kind: command.TurnStarted, WorkID: w.work().ID, ProcessID: "earlier", ObservationSequence: 1_000_000}))
	require.Equal(t, domain.WorkWorking, w.work().State)
	require.True(t, w.work().HoldsProcess())
	require.False(t, w.execution(t).Process.Exited)
	require.False(t, w.execution(t).Retired())
	require.Zero(t, w.work().HarnessProcess.Turn.Sequence)
	require.ErrorIs(t, w.feedback().Handle(ctx, command.ExecutionFeedback{Kind: command.TurnStarted, WorkID: w.work().ID, ObservationSequence: 1}), domain.ErrInvalidWork, "Dispatcher feedback names its process")
}

// An exit without an admitted stop is a failure that keeps its exit code;
// Dispatcher sends no reason for an exit it merely observed.
func TestUnrequestedExitRecordsFailureWithItsCode(t *testing.T) {
	w := runningWorld(t)
	w.exit(t, 1)
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.Equal(t, "native_process_exited_unexpectedly (exit code 1)", w.work().FailureReason)
	require.True(t, w.execution(t).Retired(), "the exited run's execution retires")
}

func TestContradictoryTurnFeedbackCannotPersist(t *testing.T) {
	w := stoppingWorld(t)
	saves := w.works.saves
	err := w.feedback().Handle(context.Background(), command.ExecutionFeedback{ProcessID: "process",
		Kind: command.TurnStarted, WorkID: w.work().ID, ObservationSequence: 1, TurnCompleted: true,
	})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	require.Equal(t, saves, w.works.saves)
}

// The exit fact is kept on the execution even when its Work cannot be saved
// now; the execution retires only once the Work recorded the exit, and the
// Work's recovery applies the kept report later.
func TestExitIsKeptWhenItsWorkCannotBeSavedNow(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	failure := errors.New("database unavailable")
	w.works.saveErr = failure
	err := w.feedback().Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutionExited, WorkID: w.work().ID})
	require.ErrorIs(t, err, failure)
	require.True(t, w.execution(t).Process.Exited, "the exit is recorded on the execution first")
	require.False(t, w.work().HarnessProcess.Exited)
	require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
	require.False(t, w.execution(t).Retired(), "no retirement before the Work recorded the exit")
	w.works.saveErr = nil
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.True(t, w.work().HarnessProcess.Exited, "the recovery applies the kept report")
	require.Equal(t, domain.WorkStopping, w.work().State)
	require.Equal(t, 1, w.works.committedOf(domain.EventKindWorkComputeReleaseRequested), "the Work asks for the retirement with the exit")
	require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
	require.Equal(t, domain.RetiredByExit, w.execution(t).Retirement, "the retirement passes once the exit is recorded")
}

// An exit fact read against an execution that changed concurrently, such as
// a stop's exit request passed meanwhile, is recorded on the execution as it
// is now, a bounded number of times.
func TestExitIsRecordedOverAConcurrentChangeOfItsExecution(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	require.NoError(t, w.work().Stop("stop", false))
	require.NoError(t, w.works.Save(ctx, w.work()))
	racing := &racingExecutions{Executions: w.executions, race: func() {
		require.NoError(t, w.coordination.RequestProcessExit(ctx, w.work().ID))
	}}
	feedback := command.NewApplyExecutionFeedbackHandler(w.now, w.works, racing, w.coordination, w.harness, w.settlements)
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: "process", ExitCode: 143}))
	require.True(t, w.execution(t).Process.Exited, "the exit is recorded after the concurrent change")
	require.Equal(t, "stop", w.execution(t).ExitRequest.StopRequestID, "the concurrent change is kept")
	require.True(t, w.work().HarnessProcess.Exited)
	require.Equal(t, domain.WorkStopping, w.work().State)

	w = stoppingWorld(t)
	w.executions.Conflicts = 3
	err := w.feedback().Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: "process"})
	require.ErrorIs(t, err, domain.ErrConcurrentModification, "the retries are bounded")
	require.False(t, w.execution(t).Process.Exited)
}

// racingExecutions runs a concurrent change once, right after the first read
// of a Work's latest execution.
type racingExecutions struct {
	*commandtest.Executions
	race func()
}

func (r *racingExecutions) Latest(ctx context.Context, workID string) (*domain.AgentExecution, error) {
	execution, err := r.Executions.Latest(ctx, workID)
	if race := r.race; race != nil {
		r.race = nil
		race()
	}
	return execution, err
}

// An authoritative loss of the bound environment fails a stopping Work
// without manufacturing an exit; confirmed absence needs no deletion, and the
// Work closes once the release is confirmed.
func TestAuthoritativeEnvironmentLossFailsStoppingWork(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	w.environment.Observation = domain.EnvironmentObservation{}
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Empty(t, w.harness.Observed, "an authoritative loss is the observation")
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.Equal(t, domain.WorkFailed, w.work().State, "missing normal exit cannot become Stopped")
	require.NotEmpty(t, w.work().FailureReason)
	require.False(t, w.work().HarnessProcess.Exited, "environment loss does not manufacture a normal exit")
	require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
	require.Equal(t, domain.RetiredByLoss, w.execution(t).Retirement)
	w.release(t)
	require.Empty(t, w.environment.Deleted, "confirmed absence needs no delete request")
	require.True(t, w.work().HarnessProcess.ComputeReclaimed)
	require.NoError(t, w.work().Close("close-after-loss", domain.ClosureFacts{}))
	require.Equal(t, domain.WorkClosed, w.work().State)
}

func TestAuthoritativeEnvironmentLossFailsWorkingWork(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	w.environment.Observation = domain.EnvironmentObservation{}
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.Equal(t, "execution environment lost", w.work().FailureReason)
	require.True(t, w.work().HarnessProcess.EnvironmentLost)
}

// A normal exit keeps the stop: the retired execution's environments are
// released, never lost.
func TestEnvironmentLossAfterNormalExitKeepsTheStop(t *testing.T) {
	w := stoppingWorld(t)
	w.exit(t, 0)
	w.release(t)
	require.Equal(t, domain.WorkStopped, w.work().State)
	require.Empty(t, w.work().FailureReason)
	require.False(t, w.execution(t).Outcome.Lost)
}

// A resumed process that becomes ready with nothing admitted is idle whichever
// readiness report arrives last; admitted input keeps it busy.
func TestReadyProcessIsIdleOnlyWhenNothingIsComing(t *testing.T) {
	ctx := context.Background()
	resumed := func(t *testing.T) *domain.AgentWork {
		session := stoppedSession(t)
		require.NoError(t, session.Resume(nextAdmission(session.AgentID)))
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
			w := newWorld(t, resumed(t))
			w.harness.Inputs = tc.buffered
			w.launch(t, "process")
			for _, kind := range tc.order {
				require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: w.work().ID}))
			}
			require.Equal(t, domain.WorkWorking, w.work().State)
			require.Equal(t, tc.idle, w.work().Idle())
		})
	}
}

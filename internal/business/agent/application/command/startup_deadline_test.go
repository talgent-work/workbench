package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// reconcileAt reconciles the Work's current execution at a time, observing
// its bound environment running, and applies what it reported to the Work,
// passing the Work's execution requests back.
func reconcileAt(t *testing.T, w *world, at time.Time) {
	t.Helper()
	ctx := context.Background()
	w.at = at
	execution := w.execution(t)
	w.environment.Observation = domain.EnvironmentObservation{Attached: true}
	if execution.Environment != "" {
		w.environment.Observation.Environments = []domain.ObservedEnvironment{{ID: execution.Environment, Running: true}}
	}
	_, err := w.reconciliation().Reconcile(ctx, execution.ID, false)
	require.NoError(t, err)
	// The execution's report event applies its reports; the execution requests they
	// leave on the Work are passed by the events the Work's save commits.
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
}

func TestStartStuckOnARequiredStartupStepFailsAtTheStartupDeadline(t *testing.T) {
	for name, tc := range map[string]struct {
		prepared bool
		reason   string
	}{
		"execution environment": {reason: "Startup Deadline reached before the execution environment was ready."},
		"Harness":               {prepared: true, reason: "Startup Deadline reached before the Harness started."},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, startingSession(t))
			w.launch(t, "process")
			if tc.prepared {
				w.change(t, func(execution *domain.AgentExecution) {
					_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true}, w.now())
					require.NoError(t, err)
				})
			}
			deadline := w.execution(t).StartupDeadlineAt()

			reconcileAt(t, w, deadline.Add(-time.Second))
			require.Equal(t, domain.WorkStarting, w.work().State, "the start still has time")

			reconcileAt(t, w, deadline)
			require.Equal(t, domain.WorkFailed, w.work().State)
			require.Equal(t, tc.reason, w.work().FailureReason)
			require.NotEmpty(t, w.execution(t).ExitRequest.StopRequestID, "the launched process is asked to end")
			reconcileAt(t, w, deadline)
			reconcileAt(t, w, deadline)
			require.Len(t, w.harness.Stops, 2, "each reconciliation signals the one exit request again until the exit is reported")
			require.Equal(t, w.harness.Stops[0], w.harness.Stops[1])
			require.True(t, w.work().HoldsProcess(), "the process is held until its end is confirmed")
		})
	}
}

func TestReadinessAfterAStartupDeadlineFailureDoesNotReviveTheWork(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	w.launch(t, "process")
	reconcileAt(t, w, w.execution(t).StartupDeadlineAt())
	require.Equal(t, domain.WorkFailed, w.work().State)

	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, w.feedback().Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: w.work().ID}))
	}
	require.Equal(t, domain.WorkFailed, w.work().State, "only an explicit Restart continues a failed Work")
	require.NotEqual(t, domain.ExecutionRunning, w.execution(t).State)
}

func TestRestartCountsItsOwnStartupDeadline(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	w := worldOver(t, sessions)
	w.launch(t, "process")
	firstDeadline := w.execution(t).StartupDeadlineAt()
	reconcileAt(t, w, firstDeadline)
	require.Equal(t, domain.WorkFailed, w.work().State)
	w.at = time.Now()
	w.exit(t, 0)
	w.release(t)

	sessions.requests = map[string]query.StartRequest{}
	_, err := restartHandler(sessions, agents, capacityCheck{}).Handle(ctx, command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	w.launch(t, "restarted")
	restarted := w.execution(t).StartupDeadlineAt()
	require.True(t, restarted.After(firstDeadline), "Restart enters Starting anew")

	reconcileAt(t, w, restarted.Add(-time.Second))
	require.Equal(t, domain.WorkStarting, sessions.session.State, "the earlier start's deadline does not bound the Restart")
	reconcileAt(t, w, restarted)
	require.Equal(t, domain.WorkFailed, sessions.session.State)
}

// A start that failed before it launched a Harness process is abandoned once
// it can no longer launch one; its execution retires and its release is
// confirmed, so the Work may restart.
func TestAbandonedStartIsReleasedThroughReconciliation(t *testing.T) {
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) { require.True(t, execution.RecordSubmission()) })
	deadline := w.execution(t).StartupDeadlineAt()
	reconcileAt(t, w, deadline)
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.False(t, w.execution(t).Retired(), "the failure alone releases nothing yet")

	reconcileAt(t, w, deadline.Add(time.Minute))
	require.True(t, w.work().HarnessProcess.StartAbandoned, "the abandonment is applied")
	require.False(t, w.work().HoldsProcess())
	require.Equal(t, domain.RetiredByAbandonment, w.execution(t).Retirement, "the abandoned execution retires")

	w.release(t)
	require.True(t, w.work().HarnessProcess.ComputeReclaimed, "the Work may now restart")
	require.NoError(t, w.work().AdmitRestart())
}

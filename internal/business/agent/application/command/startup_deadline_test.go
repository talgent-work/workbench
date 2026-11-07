package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// stopRecordingPort records the process end requested by a Work.
type stopRecordingPort struct{ stops []string }

func (*stopRecordingPort) StartExecution(context.Context, domain.StartExecutionRequest) error {
	return nil
}
func (p *stopRecordingPort) StopExecution(_ context.Context, _ string, requestID string) error {
	p.stops = append(p.stops, requestID)
	return nil
}
func (*stopRecordingPort) ReclaimCompute(context.Context, string) error { return nil }

func (*stopRecordingPort) RetireFencedRun(context.Context, string) error { return nil }

func recoverAt(t *testing.T, at time.Time, sessions *feedbackSessionStore, agents *startupAgentStore, port domain.ExecutionPort, initial command.InitialExecutionReader) {
	t.Helper()
	handler := command.NewRecoverWorkHandler(func() time.Time { return at }, sessions, port, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}, nil, nil)
	require.NoError(t, handler.Handle(context.Background(), sessions.session.ID))
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
			agents, sessions := startupStores(t)
			if tc.prepared {
				require.NoError(t, sessions.session.ConfirmPrepared(context.Background(), time.Now(), nil))
			}
			port := &stopRecordingPort{}
			deadline := sessions.session.HarnessProcess.StartingSince.Add(domain.StartupDeadline)

			recoverAt(t, deadline.Add(-time.Second), sessions, agents, port, noInitialExecution{})
			require.Equal(t, domain.WorkStarting, sessions.session.State, "the start still has time")

			recoverAt(t, deadline, sessions, agents, port, noInitialExecution{})
			require.Equal(t, domain.WorkFailed, sessions.session.State)
			require.Equal(t, tc.reason, sessions.session.FailureReason)
			require.Len(t, port.stops, 1, "the held process is asked to end")
			require.True(t, sessions.session.HoldsProcess(), "the process is held until its end is confirmed")
		})
	}
}

func TestReadinessAfterAStartupDeadlineFailureDoesNotReviveTheWork(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	recoverAt(t, sessions.session.HarnessProcess.StartingSince.Add(domain.StartupDeadline), sessions, agents, &stopRecordingPort{}, noInitialExecution{})
	require.Equal(t, domain.WorkFailed, sessions.session.State)

	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, sessions, exitedProcess(), &feedbackPort{}, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.ExecutorPrepared, WorkID: sessions.session.ID}))
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: command.AgentStarted, WorkID: sessions.session.ID}))
	require.Equal(t, domain.WorkFailed, sessions.session.State, "only an explicit Restart continues a failed Work")
}

func TestRestartCountsItsOwnStartupDeadline(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	firstDeadline := sessions.session.HarnessProcess.StartingSince.Add(domain.StartupDeadline)
	recoverAt(t, firstDeadline, sessions, agents, &stopRecordingPort{}, noInitialExecution{})
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.NoError(t, sessions.session.RecordExit(ctx, domain.HarnessExit{}, &feedbackPort{}))
	require.NoError(t, sessions.session.ConfirmComputeReclaimed())

	sessions.starts = map[string]query.AdmittedStart{}
	restart := command.NewRestartWorkHandler(time.Now, sessions, agents, sessionAccess{}, capacityCheck{}, &stopRecordingPort{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{})
	_, err := restart.Handle(ctx, command.StartWork{RequestID: uuid.NewString(), SubjectID: "owner", WorkID: sessions.session.ID})
	require.NoError(t, err)
	require.Equal(t, domain.WorkStarting, sessions.session.State)
	restarted := sessions.session.HarnessProcess.StartingSince
	require.True(t, restarted.After(firstDeadline.Add(-domain.StartupDeadline)), "Restart enters Starting anew")

	recoverAt(t, restarted.Add(domain.StartupDeadline-time.Second), sessions, agents, &stopRecordingPort{}, noInitialExecution{})
	require.Equal(t, domain.WorkStarting, sessions.session.State, "the earlier start's deadline does not bound the Restart")
	recoverAt(t, restarted.Add(domain.StartupDeadline), sessions, agents, &stopRecordingPort{}, noInitialExecution{})
	require.Equal(t, domain.WorkFailed, sessions.session.State)
}

func (*stopRecordingPort) CancelRunningTurn(context.Context, string) error { return nil }

// Through the production path, where execution requests are sent after the
// Work is saved: a start that failed before it launched a Harness process is
// reported abandoned by observation, and its environment release is sent.
func TestAbandonedStartIsReleasedThroughRecoveryAndObservation(t *testing.T) {
	ctx := context.Background()
	agents, sessions := startupStores(t)
	deadline := sessions.session.HarnessProcess.StartingSince.Add(domain.StartupDeadline)
	port := &feedbackPort{}
	recoverAt(t, deadline, sessions, agents, port, noInitialExecution{})
	require.Equal(t, domain.WorkFailed, sessions.session.State)
	require.Zero(t, port.reclaims, "the failure alone releases nothing yet")

	processes := exitedProcess()
	processes.observation = domain.ProcessObservation{State: domain.ProcessStartAbandoned}
	feedback := command.NewApplyExecutionFeedbackHandler(func() time.Time { return deadline }, sessions, processes, port, &commandtest.InputBuffer{}, &commandtest.InputSettlements{})
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{WorkID: sessions.session.ID, Kind: command.ProcessObserved}))
	require.True(t, sessions.session.HarnessProcess.StartAbandoned, "the abandonment is saved")
	require.Equal(t, 1, port.reclaims, "the release is sent after the save")
	require.False(t, sessions.session.HoldsProcess())

	processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{WorkID: sessions.session.ID, Kind: command.ComputeReclaimed}))
	require.True(t, sessions.session.HarnessProcess.ComputeReclaimed, "the Work may now restart")
}

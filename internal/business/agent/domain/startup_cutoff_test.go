package domain_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The Startup Deadline is a hard cut-off: a completion reported after it never
// completes the start, whichever path reports it.
func TestCompletionReportedAfterTheStartupDeadlineFailsTheStart(t *testing.T) {
	for name, tc := range map[string]struct {
		preparedInTime bool
		late           func(context.Context, *domain.AgentWork, time.Time, domain.ExecutionPort) error
		reason         string
	}{
		"late preparation": {
			late: func(ctx context.Context, work *domain.AgentWork, at time.Time, port domain.ExecutionPort) error {
				return work.ConfirmPrepared(ctx, at, port)
			},
			reason: "Startup Deadline reached before the execution environment was ready.",
		},
		"late Harness start": {
			preparedInTime: true,
			late: func(ctx context.Context, work *domain.AgentWork, at time.Time, port domain.ExecutionPort) error {
				return work.ConfirmStarted(ctx, at, port)
			},
			reason: "Startup Deadline reached before the Harness started.",
		},
		"late ready observation": {
			preparedInTime: true,
			late: func(ctx context.Context, work *domain.AgentWork, at time.Time, port domain.ExecutionPort) error {
				return work.ReconcileProcess(ctx, domain.ProcessObservation{State: domain.ProcessRunning, Ready: true, ObservedAt: at}, port)
			},
			reason: "Startup Deadline reached before the Harness started.",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			work := newSession(t)
			port := &executionPort{}
			started := work.HarnessProcess.StartingSince
			if tc.preparedInTime {
				require.NoError(t, work.ConfirmPrepared(ctx, started.Add(time.Minute), port))
			}
			require.NoError(t, tc.late(ctx, work, started.Add(domain.StartupDeadline+time.Second), port))
			require.Equal(t, domain.WorkFailed, work.State)
			require.Equal(t, tc.reason, work.FailureReason)
			require.False(t, work.HarnessProcess.Started)
		})
	}
}

func TestCompletionBeforeTheStartupDeadlineEstablishesWorking(t *testing.T) {
	ctx := context.Background()
	work := newSession(t)
	port := &executionPort{}
	almost := work.HarnessProcess.StartingSince.Add(domain.StartupDeadline - time.Second)
	require.NoError(t, work.ConfirmPrepared(ctx, almost, port))
	require.NoError(t, work.ConfirmStarted(ctx, almost, port))
	require.Equal(t, domain.WorkWorking, work.State)
}

// Readiness from an observation without its time cannot be judged against the
// deadline, so it is rejected rather than admitted.
func TestReadyObservationWithoutItsTimeIsRejected(t *testing.T) {
	work := newSession(t)
	err := work.ReconcileProcess(context.Background(), domain.ProcessObservation{State: domain.ProcessRunning, Ready: true}, &executionPort{})
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	require.Equal(t, domain.WorkStarting, work.State)
}

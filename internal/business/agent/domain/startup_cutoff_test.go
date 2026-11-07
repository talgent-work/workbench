package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The Startup Deadline is a hard cut-off: a readiness report at or after it
// never completes the start; the start fails naming the earliest step still
// incomplete, and its Work applies that failure.
func TestReadinessReportedAfterTheStartupDeadlineFailsTheStart(t *testing.T) {
	for name, tc := range map[string]struct {
		preparedInTime bool
		late           domain.ReadinessReport
		reason         string
	}{
		"late preparation":   {late: domain.ReadinessReport{Prepared: true}, reason: "Startup Deadline reached before the execution environment was ready."},
		"late Harness start": {preparedInTime: true, late: domain.ReadinessReport{Started: true}, reason: "Startup Deadline reached before the Harness started."},
		"late readiness":     {preparedInTime: true, late: domain.ReadinessReport{Prepared: true, Started: true}, reason: "Startup Deadline reached before the Harness started."},
	} {
		t.Run(name, func(t *testing.T) {
			work, execution := launchedExecution(t)
			seated := execution.Desk.SeatedAt
			if tc.preparedInTime {
				_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true}, seated.Add(time.Minute))
				require.NoError(t, err)
			}
			_, err := execution.ConfirmReady(tc.late, seated.Add(domain.StartupDeadline+time.Second))
			require.NoError(t, err)
			require.Equal(t, domain.ExecutionStarting, execution.State, "a late report never establishes readiness")
			require.Equal(t, tc.reason, execution.Outcome.StartFailure)
			for _, report := range execution.Reports() {
				_, err := work.ApplyExecutionReport(report)
				require.NoError(t, err)
			}
			require.Equal(t, domain.WorkFailed, work.State)
			require.Equal(t, tc.reason, work.FailureReason)
			require.False(t, work.HarnessProcess.Ready)
		})
	}
}

func TestReadinessBeforeTheStartupDeadlineEstablishesRunning(t *testing.T) {
	work, execution := launchedExecution(t)
	almost := execution.Desk.SeatedAt.Add(domain.StartupDeadline - time.Second)
	_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true}, almost)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionStarting, execution.State, "prepared alone is not ready")
	_, err = execution.ConfirmReady(domain.ReadinessReport{Started: true}, almost)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionRunning, execution.State)
	require.Equal(t, []domain.Event{domain.ExecutionReady{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt}}, executionEvents(execution))
	for _, report := range execution.Reports() {
		_, err := work.ApplyExecutionReport(report)
		require.NoError(t, err)
	}
	require.Equal(t, domain.WorkWorking, work.State)
}

// Readiness without its time cannot be judged against the deadline, so it is
// rejected rather than admitted.
func TestReadinessWithoutItsTimeIsRejected(t *testing.T) {
	_, execution := launchedExecution(t)
	_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, time.Time{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	require.Equal(t, domain.ExecutionStarting, execution.State)
}

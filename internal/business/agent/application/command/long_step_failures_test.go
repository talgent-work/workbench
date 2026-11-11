package command_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A start whose own request can never be realized is rejected on its first
// attempt, naming why, instead of being retried until its Startup Deadline.
func TestADefiniteSpecificationFailureRejectsTheStartAtOnceNamingItsCause(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.environment.SpecifyErr = domain.InvalidLaunchSpecification("Plugin release talgent 1.0.0 names its native entry talgent-runtime")

	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID), "the rejection is recorded, so nothing is left to retry")
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))

	require.Zero(t, w.environment.Submitted, "nothing is submitted for a start that cannot be specified")
	require.True(t, w.execution(t).Outcome.Rejected)
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.True(t, w.work().HarnessProcess.StartRejected)
	require.Equal(t, "The execution environment could not be created for this start: specifying the execution environment: Plugin release talgent 1.0.0 names its native entry talgent-runtime.", w.work().FailureReason)

	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Equal(t, 1, w.environment.Specified, "a rejected start is not specified again")
}

// A failure that may pass is retried; it is not taken for a rejection.
func TestATransientSpecificationFailureIsReturnedForRetry(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	unavailable := errors.New("database unavailable")
	w.environment.SpecifyErr = unavailable

	require.ErrorIs(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID), unavailable)
	require.False(t, w.execution(t).Outcome.Rejected)
	require.Equal(t, domain.WorkStarting, w.work().State)
}

// A failure that may pass is retried until the Startup Deadline, which ends
// the start naming the step that kept failing rather than only the deadline.
func TestAStartThatKeepsFailingTransientlyFailsAtItsDeadlineNamingTheLastCause(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.environment.SubmitErr = context.DeadlineExceeded
	require.ErrorIs(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID), context.DeadlineExceeded, "a lost creation answer is left to observation and the deadline")
	require.Equal(t, "creating the execution environment timed out", w.execution(t).StartupSetback)

	reconcileAt(t, w, w.execution(t).StartupDeadlineAt())
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.Equal(t, "Startup Deadline reached before the execution environment was ready. Last error: creating the execution environment timed out.", w.work().FailureReason)
}

// A definite failure once the environment is bound fails the start at once,
// naming its step and cause.
func TestADefiniteLaunchFailureFailsTheStartAtOnce(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	})
	w.environment.Observation = domain.EnvironmentObservation{Attached: true, Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true}}}
	w.environment.StepErr = domain.ErrExecutionRequest

	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.Equal(t, domain.WorkFailed, w.work().State)
	require.Equal(t, "The start could not complete: launching the Harness: execution request failed.", w.work().FailureReason)
	require.Equal(t, 1, w.environment.Launched)
}

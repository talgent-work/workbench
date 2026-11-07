//go:build integration

package execution

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// submitting starts the execution's long step, which submits the creation,
// and returns once the create reached the server; the long step's outcome
// arrives on the channel. A create still waiting when the test ends is
// abandoned, so the server's close never waits for it.
func (f *unboundStart) submitting(t *testing.T, ctx context.Context, answer chan struct{}, failure int, code string) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	f.lifecycle.mu.Lock()
	f.lifecycle.answer, f.lifecycle.failAfterCreate, f.lifecycle.refuseCode = answer, failure, code
	f.lifecycle.mu.Unlock()
	require.True(t, f.reconcile(t), "the creation's submission is a long step")
	id := f.allocation(t).ID
	done := make(chan error, 1)
	go func() { done <- f.steps.handler.RunLongStep(ctx, id) }()
	require.Eventually(t, func() bool { return f.creates() == 1 }, 5*time.Second, 10*time.Millisecond)
	require.True(t, f.allocation(t).Submitted, "the submission is recorded before the create is sent")
	return done
}

// A successful answer that arrives after a delay binds the sandbox it created;
// the create is sent once.
func TestASuccessfulCreateAnswerBindsTheSandbox(t *testing.T) {
	f := newCloudStart(t)
	answer := make(chan struct{})
	done := f.submitting(t, context.Background(), answer, 0, "")
	require.Empty(t, f.allocation(t).SandboxID, "nothing is bound while the create waits")
	close(answer)
	require.NoError(t, <-done)
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "the answer binds the created sandbox")

	require.True(t, f.reconcile(t), "onboarding continues to the preparation")
	require.True(t, f.allocation(t).SandboxReady, "the bound sandbox receives its credentials")
	require.Equal(t, 1, f.creates(), "the create is never sent twice")
}

// The server's readiness timeout deletes the sandbox it created and answers
// the create with its failure: the start is rejected at once, long before its
// Startup Deadline, and the Work fails naming its execution environment. The
// rejected start is released and restartable without a second create.
func TestADefiniteCreateFailureRejectsTheStartNamingItsEnvironment(t *testing.T) {
	f := newCloudStart(t)
	answer := make(chan struct{})
	done := f.submitting(t, context.Background(), answer, http.StatusGatewayTimeout, "KUBERNETES::POD_READY_TIMEOUT")
	require.True(t, f.sandboxPresent(), "the server created the sandbox while it waited")
	close(answer)
	require.NoError(t, <-done)
	require.False(t, f.sandboxPresent(), "the server deleted the sandbox before answering")

	run, err := f.steps.executions.Latest(context.Background(), f.workID)
	require.NoError(t, err)
	require.True(t, run.Outcome.Rejected)
	require.True(t, f.now().Before(run.StartupDeadlineAt()), "the start is rejected without waiting for its deadline")
	f.recover(t)
	work := f.work(t)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.StartRejected)
	require.Contains(t, work.FailureReason, "execution environment", "the failure names the execution environment")
	require.NotZero(t, f.allocation(t).DeletedAt, "the rejected start's execution is retired")

	f.reconcile(t)
	require.NotZero(t, f.allocation(t).ReleasedAt, "with nothing to delete, the release is confirmed at once")
	f.requireRestartable(t)
	require.Equal(t, 1, f.creates())
}

// A failure answered after the sandbox was bound changes nothing by itself:
// the sandbox's disappearance is observed as its loss.
func TestADefiniteCreateFailureAfterBindingIsObservedAsLoss(t *testing.T) {
	f := newCloudStart(t)
	answer := make(chan struct{})
	done := f.submitting(t, context.Background(), answer, http.StatusGatewayTimeout, "KUBERNETES::POD_READY_TIMEOUT")
	f.reconcile(t)
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "the observed sandbox is bound while the create waits")
	close(answer)
	require.NoError(t, <-done)

	run, err := f.steps.executions.Latest(context.Background(), f.workID)
	require.NoError(t, err)
	require.False(t, run.Outcome.Rejected, "a bound start is never rejected")
	require.True(t, run.Outcome.Lost, "the deleted sandbox is lost")
	f.recover(t)
	work := f.work(t)
	require.Equal(t, domain.WorkFailed, work.State)
	require.True(t, work.HarnessProcess.EnvironmentLost)
	require.False(t, work.HarnessProcess.StartRejected)
	require.Contains(t, work.FailureReason, "execution environment", "the failure names the execution environment")
	require.Equal(t, 1, f.creates())
}

// A create never answered leaves observation to decide: the sandbox is bound
// as soon as it is observed and onboarding continues while the create still
// waits; the lost answer sends nothing again.
func TestALostCreateAnswerFallsBackToObservation(t *testing.T) {
	f := newCloudStart(t)
	waiting, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := f.submitting(t, waiting, make(chan struct{}), 0, "")
	require.True(t, f.reconcile(t), "onboarding continues to the preparation")
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "the observed sandbox is bound without an answer")
	require.True(t, f.allocation(t).SandboxReady, "the bound sandbox receives its credentials")
	cancel()
	require.ErrorIs(t, <-done, context.Canceled, "the answer is lost")
	require.Equal(t, sandboxID, f.allocation(t).SandboxID, "a lost answer leaves the binding")
	require.True(t, f.reconcile(t))
	require.Equal(t, 1, f.creates(), "a lost answer never sends the create again")
}

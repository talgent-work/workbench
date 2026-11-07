//go:build integration

package execution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// runningCloudStart is a Cloud Work whose sandbox was created and whose run
// is ready, its process read by Dispatcher at the sandbox, the Work idle.
func runningCloudStart(t *testing.T, configure func(*lifecycleFixture)) *cloudStart {
	t.Helper()
	ctx := context.Background()
	f := newCloudStart(t)
	f.lifecycle.mu.Lock()
	configure(f.lifecycle)
	f.lifecycle.mu.Unlock()
	require.True(t, f.reconcile(t), "the created sandbox is bound and prepared next")
	run, err := f.steps.executions.Latest(ctx, f.workID)
	require.NoError(t, err)
	require.NotEmpty(t, run.Environment)
	_, err = run.RecordPreparation(domain.PreparationReport{})
	require.NoError(t, err)
	_, err = run.RecordLaunch("process-1")
	require.NoError(t, err)
	_, err = run.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, f.now())
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(ctx, run))
	f.readings.set(f.workID, StreamReading{ProcessID: "process-1", Endpoint: "http://" + f.endpoint()})
	f.recover(t)
	require.Equal(t, domain.WorkWorking, f.work(t).State)
	return f
}

func (f *cloudStart) endpoint() string {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return f.lifecycle.endpoint
}

// renewals are the expiries the fixture's renewals asked for.
func (f *cloudStart) renewals() []time.Time {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	return append([]time.Time(nil), f.lifecycle.renewals...)
}

// createTimeout is the expiry, in seconds, the create asked for; zero for
// none.
func (f *cloudStart) createTimeout() int {
	f.lifecycle.mu.Lock()
	defer f.lifecycle.mu.Unlock()
	if f.lifecycle.create.Timeout == nil {
		return 0
	}
	return *f.lifecycle.create.Timeout
}

// lease is the execution's recorded lease.
func (f *cloudStart) lease(t *testing.T) time.Time {
	t.Helper()
	return fromMillis(f.allocation(t).LeaseExpiresAt)
}

// The create asks for the lease duration and the execution records the
// expiry the server returned. A lease far from expiry is left alone; below
// the renewal threshold it is renewed to the lease duration from now, which
// only extends it, and the returned expiry is recorded.
func TestASandboxIsCreatedLeasedAndRenewedBelowTheThreshold(t *testing.T) {
	f := runningCloudStart(t, func(*lifecycleFixture) {})
	created := f.now()
	require.Equal(t, int(fixtureLease.Duration/time.Second), f.createTimeout(), "the create carries the expiry")
	require.Equal(t, created.Add(fixtureLease.Duration).Truncate(time.Millisecond).UTC(), f.lease(t), "the returned expiry is recorded")

	f.advance(59 * time.Minute)
	f.reconcile(t)
	require.Empty(t, f.renewals(), "a lease with more than the threshold left is not renewed")

	f.advance(2 * time.Minute)
	before := f.lease(t)
	f.reconcile(t)
	renewals := f.renewals()
	require.Len(t, renewals, 1)
	require.True(t, renewals[0].After(before), "a renewal only extends the lease")
	require.Equal(t, f.now().Add(fixtureLease.Duration).Truncate(time.Millisecond).UTC(), f.lease(t), "the renewed expiry is recorded")

	f.reconcile(t)
	require.Len(t, f.renewals(), 1, "a renewed lease is left alone")
}

// A running Work that nothing wakes, its Turn working for hours, keeps its
// sandbox: every sweep's reconciliation renews the lease before the
// controller would delete it.
func TestAnUnwokenRunningWorkKeepsItsSandbox(t *testing.T) {
	f := runningCloudStart(t, func(*lifecycleFixture) {})
	work := f.work(t)
	require.NoError(t, work.ObserveTurn(domain.TurnObservation{Sequence: 1, Active: true}, false, false, f.now()))
	require.NoError(t, f.works.Save(context.Background(), work))
	for range 12 {
		f.advance(30 * time.Minute)
		f.reconcile(t)
		f.recover(t)
	}
	require.True(t, f.sandboxPresent(), "the sandbox outlives its first lease")
	require.GreaterOrEqual(t, len(f.renewals()), 5)
	require.Equal(t, domain.WorkWorking, f.work(t).State)
	require.False(t, f.allocation(t).EnvironmentLost)
}

// A sandbox created without an expiry, as before leasing, is never renewed
// and keeps working until its run ends.
func TestASandboxWithoutARecordedExpiryIsNotRenewed(t *testing.T) {
	f := runningCloudStart(t, func(l *lifecycleFixture) { l.unleased = true })
	require.Zero(t, f.allocation(t).LeaseExpiresAt, "no expiry is known")
	f.advance(5 * time.Hour)
	f.reconcile(t)
	require.Empty(t, f.renewals())
	require.True(t, f.sandboxPresent())
	require.Equal(t, domain.WorkWorking, f.work(t).State)
}

// A sandbox whose lease ran out is deleted by the controller, and its
// execution observes the loss as an ordinary environment loss: its Work
// fails, the execution is retired and released, and Restart is admitted.
func TestAnExpiredSandboxIsObservedAsLost(t *testing.T) {
	f := runningCloudStart(t, func(*lifecycleFixture) {})
	f.advance(fixtureLease.Duration)
	f.reconcile(t)
	require.False(t, f.sandboxPresent(), "the controller deleted the expired sandbox")
	require.True(t, f.allocation(t).EnvironmentLost, "the expiry is observed as the environment's loss")
	f.recover(t)
	require.Equal(t, domain.WorkFailed, f.work(t).State)
	f.reconcile(t)
	f.requireRestartable(t)
}

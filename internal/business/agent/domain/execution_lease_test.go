package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// leaseTerms are the deployment's lease terms in these cases: two-hour
// leases renewed once less than an hour remains.
var leaseTerms = domain.LeaseTerms{Duration: 2 * time.Hour, RenewalThreshold: time.Hour}

// leasedEnvironment observes env running with its lease expiring at expiry,
// under leaseTerms.
func leasedEnvironment(env string, expiry time.Time) domain.EnvironmentObservation {
	return domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: env, Running: true, LeaseExpiresAt: expiry}}, Lease: leaseTerms}
}

// runningExecution is a launched execution whose run was confirmed ready,
// so its Startup Deadline no longer judges it.
func runningExecution(t *testing.T) (*domain.AgentWork, *domain.AgentExecution) {
	t.Helper()
	work, execution := launchedExecution(t)
	_, err := execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, execution.Desk.SeatedAt)
	require.NoError(t, err)
	require.Equal(t, domain.ExecutionRunning, execution.State)
	return work, execution
}

// renewals lists the lease renewals among the decided steps.
func renewals(decision domain.ExecutionDecision) []domain.ExecutionStep {
	var steps []domain.ExecutionStep
	for _, step := range decision.Steps {
		if step.Kind == domain.StepRenewLease {
			steps = append(steps, step)
		}
	}
	return steps
}

// The bound environment's observed expiry is the execution's lease; a lease
// far from expiry is left alone, and one with less than the renewal threshold
// left is renewed to the lease duration from now.
func TestALeaseNearExpiryIsRenewedAndOneFarFromItIsNot(t *testing.T) {
	_, execution := runningExecution(t)
	now := execution.Desk.SeatedAt
	expiry := now.Add(2 * time.Hour)

	far := decide(t, execution, leasedEnvironment("env-1", expiry), now)
	require.True(t, far.Changed, "the observed expiry is recorded")
	require.Equal(t, expiry, execution.Lease)
	require.Empty(t, renewals(far))

	near := expiry.Add(-time.Hour + time.Second)
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepRenewLease, LeaseExpiresAt: near.Add(2 * time.Hour)}}, renewals(decide(t, execution, leasedEnvironment("env-1", expiry), near)))

	// With exactly the renewal threshold left, it still waits.
	require.Empty(t, renewals(decide(t, execution, leasedEnvironment("env-1", expiry), expiry.Add(-time.Hour))))
}

// Recording a lease keeps the later of the returned and the recorded expiry,
// so neither a stale observation nor a shorter answer ever shortens it.
func TestRecordingALeaseOnlyEverExtendsIt(t *testing.T) {
	_, execution := runningExecution(t)
	now := execution.Desk.SeatedAt
	require.True(t, execution.RecordLease(now.Add(2*time.Hour)))
	require.False(t, execution.RecordLease(now.Add(time.Hour)), "a shorter expiry is ignored")
	require.False(t, execution.RecordLease(time.Time{}))
	require.Equal(t, now.Add(2*time.Hour), execution.Lease)

	stale := decide(t, execution, leasedEnvironment("env-1", now.Add(30*time.Minute)), now)
	require.False(t, stale.Changed, "a stale observation keeps the recorded lease")
	require.Equal(t, now.Add(2*time.Hour), execution.Lease)
	require.True(t, execution.RecordLease(now.Add(3*time.Hour)))
	require.Equal(t, now.Add(3*time.Hour), execution.Lease)
}

// An environment created without an expiry carries no lease: it is never
// renewed, however long it runs.
func TestAnEnvironmentWithoutARecordedExpiryIsNeverRenewed(t *testing.T) {
	_, execution := runningExecution(t)
	decision := decide(t, execution, leasedEnvironment("env-1", time.Time{}), execution.Desk.SeatedAt.Add(24*time.Hour))
	require.Empty(t, renewals(decision))
	require.True(t, execution.Lease.IsZero())
}

// Nothing is renewed for an execution with no bound environment, nor for one
// that retired, whose environments are deleted instead; without lease terms,
// as on a Managed Computer, nothing is renewed either.
func TestALeaseIsRenewedOnlyForABoundUnretiredExecution(t *testing.T) {
	unbound := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, unbound.RecordSubmission())
	require.False(t, unbound.RecordLease(time.Now().Add(time.Hour)), "an unbound execution has no lease to record")
	require.Empty(t, renewals(decide(t, unbound, domain.EnvironmentObservation{Lease: leaseTerms}, unbound.Desk.SeatedAt)))

	work, retired := runningExecution(t)
	now := retired.Desk.SeatedAt
	decide(t, retired, leasedEnvironment("env-1", now.Add(2*time.Hour)), now)
	current, _ := retired.RecordExit("process-1", domain.HarnessExit{})
	require.True(t, current)
	changed, err := retired.Retire(domain.WorkComputeReleaseRequested{WorkID: work.ID, StartAdmittedAt: retired.StartAdmittedAt, Cause: domain.RetiredByExit}, now)
	require.NoError(t, err)
	require.True(t, changed)
	decision := decide(t, retired, leasedEnvironment("env-1", now.Add(2*time.Hour)), now.Add(90*time.Minute))
	require.Empty(t, renewals(decision))
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-1"}}, decision.Steps)

	_, unleased := runningExecution(t)
	at := unleased.Desk.SeatedAt
	observation := leasedEnvironment("env-1", at.Add(time.Minute))
	observation.Lease = domain.LeaseTerms{}
	require.Empty(t, renewals(decide(t, unleased, observation, at)))
}

// A run whose Work asked it to exit still wants its environment until it
// retires, so its lease is still renewed.
func TestAnExecutionAskedToExitKeepsRenewingItsLease(t *testing.T) {
	work, execution := runningExecution(t)
	now := execution.Desk.SeatedAt
	_, err := execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, StopRequestID: "stop-1"})
	require.NoError(t, err)
	decision := decide(t, execution, leasedEnvironment("env-1", now.Add(30*time.Minute)), now)
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepRenewLease, LeaseExpiresAt: now.Add(2 * time.Hour)}}, renewals(decision))
}

// Lease terms renew a lease only before it runs out: their renewal threshold
// is positive and below their duration.
func TestLeaseTermsNeedAThresholdBelowTheirDuration(t *testing.T) {
	require.NoError(t, leaseTerms.Validate())
	for _, terms := range []domain.LeaseTerms{{}, {Duration: time.Hour}, {Duration: time.Hour, RenewalThreshold: time.Hour}, {Duration: time.Hour, RenewalThreshold: 2 * time.Hour}} {
		require.ErrorIs(t, terms.Validate(), domain.ErrInvalidLeaseTerms, "%+v", terms)
	}
}

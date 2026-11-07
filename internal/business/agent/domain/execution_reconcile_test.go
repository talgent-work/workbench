package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Queued execution decides nothing until its Desk is seated; its first
// decision once seated is its creation, unless the first start's preparation
// still holds it back. Reconciling without a time is refused.
func TestQueuedExecutionWaitsAndASeatedOneSubmitsItsCreation(t *testing.T) {
	work, _ := queuedWork(t, time.Now())
	execution := beginOf(t, work, domain.RealizationManaged)
	require.Empty(t, decide(t, execution, runningEnvironment("env-1"), time.Now().Add(time.Hour)).Steps)
	require.False(t, execution.RecordSubmission(), "a Queued execution submits nothing")

	seatedAt := time.Now().UTC()
	seated, err := execution.Seat(seatedAt)
	require.NoError(t, err)
	require.True(t, seated)
	require.Equal(t, domain.ExecutionStarting, execution.State)
	require.Equal(t, seatedAt.Add(domain.StartupDeadline), execution.StartupDeadlineAt())
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSubmitCreation}, kinds(decide(t, execution, domain.EnvironmentObservation{}, seatedAt)))

	_, err = execution.Reconcile(domain.EnvironmentObservation{}, time.Time{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
}

// A submitted creation that produced nothing waits within the Startup
// Deadline; past it the start fails, and once it can no longer launch a
// Harness process it is abandoned.
func TestASubmittedCreationThatProducedNothingFailsThenAbandonsTheStart(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	require.True(t, execution.RecordSubmission())
	deadline := execution.StartupDeadlineAt()

	waiting := decide(t, execution, domain.EnvironmentObservation{}, deadline.Add(-time.Second))
	require.Empty(t, waiting.Steps)
	require.False(t, waiting.Changed)

	failed := decide(t, execution, domain.EnvironmentObservation{}, deadline)
	require.Empty(t, failed.Steps)
	require.True(t, failed.Changed)
	require.NotEmpty(t, execution.Outcome.StartFailure)
	require.False(t, execution.Outcome.Abandoned, "a launch begun just before the deadline may still report")
	events := executionEvents(execution)
	require.Len(t, events, 1)
	require.IsType(t, domain.ExecutionStartFailed{}, events[0])

	require.True(t, decide(t, execution, domain.EnvironmentObservation{}, deadline.Add(time.Hour)).Changed)
	require.True(t, execution.Outcome.Abandoned)
	require.Equal(t, []domain.Event{domain.ExecutionStartAbandoned{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt}}, executionEvents(execution))
	require.False(t, decide(t, execution, domain.EnvironmentObservation{}, deadline.Add(2*time.Hour)).Changed, "each outcome is reported once")
}

// A start never submitted by its Startup Deadline, or asked to exit before
// anything was created, is abandoned without a creation.
func TestAStartThatCreatedNothingIsAbandonedWithoutACreation(t *testing.T) {
	late := beginOf(t, newSession(t), domain.RealizationCloud)
	require.Empty(t, decide(t, late, domain.EnvironmentObservation{}, late.StartupDeadlineAt()).Steps)
	require.True(t, late.Outcome.Abandoned)
	require.NotEmpty(t, late.Outcome.StartFailure)

	work := newSession(t)
	stopped := beginOf(t, work, domain.RealizationCloud)
	_, err := stopped.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: stopped.StartAdmittedAt, StopRequestID: "stop-1"})
	require.NoError(t, err)
	require.Empty(t, decide(t, stopped, domain.EnvironmentObservation{}, stopped.Desk.SeatedAt).Steps)
	require.True(t, stopped.Outcome.Abandoned)
	require.Empty(t, stopped.Outcome.StartFailure, "an abandoned stop is not a failure")
	require.False(t, stopped.RecordSubmission())
}

// With none bound, the first observed environment is bound whatever its
// creation answered, a running one first, and every other one it owns is
// deleted; once bound, every surplus environment is deleted.
func TestObservedEnvironmentsBindOneAndDeleteTheSurplus(t *testing.T) {
	execution := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, execution.RecordSubmission())
	observation := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-a"}, {ID: "env-b", Running: true}, {ID: "env-c"}}}
	decision := decide(t, execution, observation, execution.Desk.SeatedAt)
	require.True(t, decision.Changed)
	require.Equal(t, "env-b", execution.Environment)
	require.Equal(t, []domain.ExecutionStep{
		{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-a"},
		{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-c"},
		{Kind: domain.StepDeliverCredentials},
	}, decision.Steps)

	changed, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-c"})
	require.NoError(t, err)
	require.False(t, changed, "the observed binding stands")
	require.Equal(t, "env-b", execution.Environment)

	pending := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, pending.RecordSubmission())
	require.Empty(t, decide(t, pending, domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-x"}}}, pending.Desk.SeatedAt).Steps, "a bound environment not yet running waits")
	require.Equal(t, "env-x", pending.Environment)
}

// A bound environment no longer observed is lost and reported once; so is
// every environment of a removed Managed Computer.
func TestABoundEnvironmentNoLongerObservedIsLost(t *testing.T) {
	work, execution := launchedExecution(t)
	decision := decide(t, execution, domain.EnvironmentObservation{LossReason: "gone"}, time.Now())
	require.Empty(t, decision.Steps)
	require.True(t, decision.Changed)
	require.True(t, execution.Outcome.Lost)
	require.Equal(t, []domain.Event{domain.ExecutionEnvironmentLost{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, Reason: "gone"}}, executionEvents(execution))
	require.False(t, decide(t, execution, runningEnvironment("env-1"), time.Now()).Changed, "a lost environment stays lost")

	_, removed := launchedExecution(t)
	decide(t, removed, domain.EnvironmentObservation{Removed: true, LossReason: domain.ComputerRemovedReason}, time.Now())
	require.True(t, removed.Outcome.Lost)
	require.Equal(t, domain.ComputerRemovedReason, removed.Outcome.LossReason)
}

// A launched run connects while Dispatcher does not read it, replaces its
// credentials past their expiry or after the model key rotated, and a
// Managed run syncs its input copies when due.
func TestARunningRunConnectsAndKeepsItsCredentialsAndInputsCurrent(t *testing.T) {
	_, execution := launchedExecution(t)
	now := execution.Desk.SeatedAt
	require.Equal(t, []domain.ExecutionStepKind{domain.StepConnect}, kinds(decide(t, execution, runningEnvironment("env-1"), now)))
	attached := runningEnvironment("env-1")
	attached.Attached = true
	attached.ModelDigest = "model-1"
	require.Empty(t, decide(t, execution, attached, now).Steps)
	attached.ModelDigest = "model-2"
	require.Equal(t, []domain.ExecutionStepKind{domain.StepDeliverCredentials}, kinds(decide(t, execution, attached, now)))

	expiring := beginOf(t, newSession(t), domain.RealizationCloud)
	require.True(t, expiring.RecordSubmission())
	_, err := expiring.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-1"})
	require.NoError(t, err)
	expiry := expiring.Desk.SeatedAt.Add(time.Hour)
	require.True(t, expiring.RecordCredentialDelivery(domain.CredentialDelivery{ExpiresAt: expiry}))
	require.True(t, recordPreparation(t, expiring))
	_, err = expiring.RecordLaunch("process-1")
	require.NoError(t, err)
	current := runningEnvironment("env-1")
	current.Attached = true
	require.Empty(t, decide(t, expiring, current, expiry.Add(-time.Second)).Steps)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepDeliverCredentials}, kinds(decide(t, expiring, current, expiry)))

	managed := beginOf(t, newSession(t), domain.RealizationManaged)
	require.True(t, managed.RecordSubmission())
	_, err = managed.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-m"})
	require.NoError(t, err)
	require.True(t, managed.RecordCredentialDelivery(domain.CredentialDelivery{}))
	syncedAt := managed.Desk.SeatedAt
	observed := runningEnvironment("env-m")
	observed.InputsRefresh = time.Minute
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSyncInputs, domain.StepPrepare}, kinds(decide(t, managed, observed, syncedAt)), "never synced inputs are due")
	require.True(t, managed.RecordInputSync(0, syncedAt))
	require.Equal(t, []domain.ExecutionStepKind{domain.StepPrepare}, kinds(decide(t, managed, observed, syncedAt)))
	require.True(t, managed.MarkInputsChanged())
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSyncInputs, domain.StepPrepare}, kinds(decide(t, managed, observed, syncedAt)))
	require.True(t, managed.RecordInputSync(managed.Inputs.Due, syncedAt))
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSyncInputs, domain.StepPrepare}, kinds(decide(t, managed, observed, syncedAt.Add(time.Minute))), "synced copies past their refresh are due")
}

// A seating needs its time and applies once, only to a Queued execution.
func TestSeatAppliesOnceToAQueuedExecution(t *testing.T) {
	work, _ := queuedWork(t, time.Now())
	execution := beginOf(t, work, domain.RealizationManaged)
	_, err := execution.Seat(time.Time{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	first := time.Now().UTC()
	seated, err := execution.Seat(first)
	require.NoError(t, err)
	require.True(t, seated)
	seated, err = execution.Seat(first.Add(time.Minute))
	require.NoError(t, err)
	require.False(t, seated, "a repeated seating changes nothing")
	require.Equal(t, first, execution.Desk.SeatedAt)

	seatedAtAdmission := beginOf(t, newSession(t), domain.RealizationCloud)
	seated, err = seatedAtAdmission.Seat(time.Now())
	require.NoError(t, err)
	require.False(t, seated, "a start seated at admission is not seated again")
}

// The end of preparation is recorded once, only for a bound, unretired
// execution.
func TestRecordPreparationNeedsABoundUnretiredExecution(t *testing.T) {
	unbound := beginOf(t, newSession(t), domain.RealizationCloud)
	require.False(t, recordPreparation(t, unbound), "nothing is prepared without a bound environment")

	_, bound := boundExecution(t)
	require.True(t, recordPreparation(t, bound))
	require.False(t, recordPreparation(t, bound), "preparation ends once")

	_, retiring := boundExecution(t)
	decide(t, retiring, domain.EnvironmentObservation{LossReason: "gone"}, time.Now())
	retired, err := retiring.Retire(domain.WorkComputeReleaseRequested{WorkID: retiring.WorkID, StartAdmittedAt: retiring.StartAdmittedAt, Cause: domain.RetiredByLoss}, time.Now())
	require.NoError(t, err)
	require.True(t, retired)
	require.False(t, recordPreparation(t, retiring), "a retired execution prepares nothing")
}

package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// creationWait is the deployment's creation wait in these tests.
const creationWait = domain.CreationWait(5*time.Minute + 30*time.Second)

// unansweredExecution is a seated Cloud execution of a new Work that
// submitted its creation at its seating, never received the answer, and bound
// the observed environment "env-1"; it is then retired as unreached.
func unansweredExecution(t *testing.T) *domain.AgentExecution {
	t.Helper()
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	submitted, err := execution.RecordSubmission(execution.Desk.SeatedAt, creationWait)
	require.NoError(t, err)
	require.True(t, submitted)
	decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)
	require.Equal(t, "env-1", execution.Environment, "the observed environment is bound without the answer")
	retired, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: execution.WorkID, StartAdmittedAt: execution.StartAdmittedAt, Cause: domain.RetiredUnreached}, execution.Desk.SeatedAt)
	require.NoError(t, err)
	require.True(t, retired)
	return execution
}

// The submission fixes the creation deadline: submission time plus the
// configured creation wait; a wait that leaves no room within the Startup
// Deadline is not configuration a submission accepts.
func TestASubmissionRecordsItsCreationDeadline(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	at := execution.Desk.SeatedAt
	for _, invalid := range []domain.CreationWait{0, -1, domain.CreationWait(domain.StartupDeadline)} {
		_, err := execution.RecordSubmission(at, invalid)
		require.ErrorIs(t, err, domain.ErrInvalidExecution, invalid)
	}
	_, err := execution.RecordSubmission(time.Time{}, creationWait)
	require.ErrorIs(t, err, domain.ErrInvalidExecution)

	submitted, err := execution.RecordSubmission(at, creationWait)
	require.NoError(t, err)
	require.True(t, submitted)
	require.Equal(t, at.UTC(), execution.Creation.SubmittedAt)
	require.Equal(t, at.UTC().Add(time.Duration(creationWait)), execution.Creation.Deadline)
	require.False(t, execution.Creation.Answered)
	require.True(t, execution.Creation.Outstanding(at))
	require.False(t, execution.Creation.Outstanding(execution.Creation.Deadline), "the creation can no longer produce an environment at its deadline")

	again, err := execution.RecordSubmission(at.Add(time.Minute), creationWait)
	require.NoError(t, err)
	require.False(t, again, "a creation is submitted once")
	require.Equal(t, at.UTC(), execution.Creation.SubmittedAt)
}

// A deleted environment whose Pod still runs keeps its retired execution
// from leaving its Desk and from being released, and is never deleted again;
// once the Pod is gone the release proceeds (Ending environment).
func TestReleaseWaitsForAnEndingEnvironment(t *testing.T) {
	work, execution := launchedExecution(t)
	execution.RecordExit("process-1", domain.HarnessExit{})
	for _, r := range execution.Reports() {
		report(t, work, r)
	}
	requested, _ := executionRequest[domain.WorkComputeReleaseRequested](work)
	retired, err := execution.Retire(requested, time.Now())
	require.NoError(t, err)
	require.True(t, retired)

	ending := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-1", Ending: true}}}
	require.Empty(t, decide(t, execution, ending, time.Now()).Steps, "an ending environment is not deleted again and its Desk stays")
	require.True(t, execution.RecordDeskLeft(time.Now()))
	confirmed, err := execution.ConfirmRelease(ending, time.Now())
	require.NoError(t, err)
	require.False(t, confirmed, "a Pod still running holds the release")
	require.Equal(t, domain.ExecutionRetired, execution.State)

	confirmed, err = execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.True(t, confirmed, "the Pod's exit releases it")
}

// While unretired, an execution never binds an ending environment, never
// deletes one, and loses its bound environment once it is ending.
func TestAnUnretiredExecutionNeitherBindsNorDeletesAnEndingEnvironment(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	submitted, err := execution.RecordSubmission(execution.Desk.SeatedAt, creationWait)
	require.NoError(t, err)
	require.True(t, submitted)
	observed := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-old", Ending: true}, {ID: "env-1", Running: true}}}
	decision := decide(t, execution, observed, execution.Desk.SeatedAt)
	require.Equal(t, "env-1", execution.Environment, "the live environment is bound")
	require.Equal(t, []domain.ExecutionStepKind{domain.StepDeliverCredentials}, kinds(decision), "the ending one is not deleted")

	_, bound := boundExecution(t)
	decide(t, bound, domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-1", Ending: true}}}, bound.Desk.SeatedAt)
	require.True(t, bound.Outcome.Lost, "a bound environment that is ending is lost")
}

// A retired execution whose creation answer never arrived is released only
// after its creation deadline, when that creation can no longer produce an
// environment (Outstanding creation).
func TestAnUnansweredCreationHoldsReleaseUntilItsDeadline(t *testing.T) {
	execution := unansweredExecution(t)
	deadline := execution.Creation.Deadline
	before := deadline.Add(-time.Second)

	require.Empty(t, kinds(decide(t, execution, domain.EnvironmentObservation{}, before)), "its Desk stays while the creation may still produce a sandbox")
	require.True(t, execution.RecordDeskLeft(before))
	confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, before)
	require.NoError(t, err)
	require.False(t, confirmed, "an outstanding creation holds the release")

	confirmed, err = execution.ConfirmRelease(domain.EnvironmentObservation{}, deadline)
	require.NoError(t, err)
	require.True(t, confirmed, "past its deadline the creation produces nothing more")
}

// An answered creation holds nothing: the retired execution leaves its Desk
// and is released as soon as no environment remains.
func TestAnAnsweredCreationIsReleasedAtOnce(t *testing.T) {
	execution := unansweredExecution(t)
	now := execution.Desk.SeatedAt.Add(time.Second)
	changed, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "env-1"})
	require.NoError(t, err)
	require.True(t, changed, "a late answer still ends the outstanding creation")
	require.Equal(t, domain.ExecutionRetired, execution.State, "the answer binds nothing once retired")

	require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, now)))
	require.True(t, execution.RecordDeskLeft(now))
	confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, now)
	require.NoError(t, err)
	require.True(t, confirmed)
}

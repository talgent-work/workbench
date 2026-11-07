package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// An execution begins from its Work's start intent: seated at once when its
// Desk was seated at admission, Queued while the Desk waits in its
// Computer's line. A start without a Desk or its admission time begins none.
func TestBeginExecutionSeatsOrQueuesItsStart(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	require.Equal(t, domain.ExecutionStarting, execution.State)
	require.Equal(t, work.ID, execution.WorkID)
	require.Equal(t, work.HarnessProcess.StartingSince.UTC(), execution.StartAdmittedAt)
	require.Equal(t, execution.StartAdmittedAt, execution.Desk.SeatedAt, "a seated start counts its deadline from its admission")
	require.Equal(t, "desk-test", execution.Desk.ID)
	require.Equal(t, execution.Desk.SeatedAt.Add(domain.StartupDeadline-2*time.Minute), execution.Launch.PreparationBound)
	require.False(t, execution.Creation.Submitted)
	require.NoError(t, execution.Validate())

	queued, _ := queuedWork(t, time.Now())
	waiting := beginOf(t, queued, domain.RealizationManaged)
	require.Equal(t, domain.ExecutionQueued, waiting.State)
	require.True(t, waiting.Desk.SeatedAt.IsZero())
	require.True(t, waiting.Launch.PreparationBound.IsZero())

	admitted, _ := intent[domain.WorkStartAdmitted](work)
	for name, broken := range map[string]func(*domain.WorkStartAdmitted){
		"no Desk":           func(a *domain.WorkStartAdmitted) { a.DeskID = "" },
		"no admission time": func(a *domain.WorkStartAdmitted) { a.StartAdmittedAt = time.Time{} },
		"another Work":      func(a *domain.WorkStartAdmitted) { a.WorkID = "other" },
	} {
		start := admitted
		broken(&start)
		_, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: start, Launch: work.LaunchSpecification(), Realization: domain.RealizationCloud})
		require.ErrorIs(t, err, domain.ErrInvalidExecution, name)
	}
}

// A refused creation while nothing is bound rejects the start and reports it;
// a refusal after binding changes nothing, since the bound environment's loss
// is observed instead. An answer naming neither is invalid.
func TestCreationAnswerBindsOrRejects(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	require.True(t, execution.RecordSubmission())
	require.False(t, execution.RecordSubmission(), "a creation is submitted once")
	_, err := execution.RecordCreationAnswer(domain.CreationAnswer{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	changed, err := execution.RecordCreationAnswer(domain.CreationAnswer{Refused: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, execution.Outcome.Rejected)
	require.Equal(t, []domain.Event{domain.ExecutionStartRejected{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt}}, executionEvents(execution))
	require.Empty(t, decide(t, execution, runningEnvironment("late"), time.Now()).Steps, "a rejected start waits for its Work to retire it")

	_, bound := boundExecution(t)
	changed, err = bound.RecordCreationAnswer(domain.CreationAnswer{Refused: true})
	require.NoError(t, err)
	require.False(t, changed)
	require.False(t, bound.Outcome.Rejected)
	require.Equal(t, "env-1", bound.Environment)
}

// Its Work's exit request is recorded once, keeps its first stop identity,
// and may add the Waiting Turn's cancellation; a request about another start
// or a retired execution changes nothing.
func TestRequestExitKeepsTheFirstStopIdentity(t *testing.T) {
	work, execution := launchedExecution(t)
	start := execution.StartAdmittedAt
	changed, err := execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start, StopRequestID: "stop-1"})
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start, StopRequestID: "stop-2", CancelWaitingTurn: true})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ExitRequest{StopRequestID: "stop-1", CancelWaitingTurn: true}, execution.ExitRequest)
	changed, err = execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start.Add(time.Second), StopRequestID: "stop-3"})
	require.NoError(t, err)
	require.False(t, changed, "a later start's request is not this execution's")
	early, other := launchedExecution(t)
	changed, err = other.RequestExit(domain.WorkStopRequested{WorkID: early.ID, StartAdmittedAt: other.StartAdmittedAt.Add(-time.Second), StopRequestID: "stop-early"})
	require.NoError(t, err)
	require.False(t, changed, "an earlier start's request, delivered late, is not this execution's")
	_, err = execution.RequestExit(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	require.Equal(t, domain.ExecutionStarting, execution.State, "an exit request is not retirement")

	// The run's credentials are withdrawn and its process asked to exit; the
	// Waiting Turn's cancellation goes with the signal.
	require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}) == false, "nothing is delivered after the exit request")
	require.Equal(t, []domain.ExecutionStepKind{domain.StepWithdrawCredentials, domain.StepSignalExit}, kinds(decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)))
	require.True(t, execution.RecordCredentialsWithdrawn())
	require.Equal(t, []domain.ExecutionStepKind{domain.StepSignalExit}, kinds(decide(t, execution, runningEnvironment("env-1"), execution.Desk.SeatedAt)))
	launched, err := execution.RecordLaunch("process-2")
	require.NoError(t, err)
	require.False(t, launched, "a second process is never launched")
}

// An exit fact acknowledges only the launched process: one naming another
// process changes nothing, and neither does a repeated one.
func TestRecordExitAcknowledgesOnlyItsProcess(t *testing.T) {
	work, execution := launchedExecution(t)
	current, changed := execution.RecordExit("other", domain.HarnessExit{ExitCode: 1})
	require.False(t, current)
	require.False(t, changed)
	current, changed = execution.RecordExit("process-1", domain.HarnessExit{ExitCode: 3, Reason: "crash"})
	require.True(t, current)
	require.True(t, changed)
	require.Equal(t, []domain.Event{domain.ExecutionExited{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt, Exit: domain.HarnessExit{ExitCode: 3, Reason: "crash"}}}, executionEvents(execution))
	current, changed = execution.RecordExit("process-1", domain.HarnessExit{})
	require.True(t, current, "a repeated fact is still acknowledged")
	require.False(t, changed)

	for _, r := range execution.Reports() {
		report(t, work, r)
	}
	require.Equal(t, domain.WorkFailed, work.State, "an exit before readiness is a failure")
	require.Equal(t, domain.RetiredByExit, releaseCause(work))
}

// An execution retires only for a cause its own facts agree with, and only
// for the start it serves.
func TestRetireRequiresACauseItsFactsAgreeWith(t *testing.T) {
	work, execution := launchedExecution(t)
	start := execution.StartAdmittedAt
	retire := func(cause domain.RetirementCause, at time.Time) bool {
		retired, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: work.ID, StartAdmittedAt: at, Cause: cause}, time.Now())
		require.NoError(t, err)
		return retired
	}
	for _, cause := range []domain.RetirementCause{domain.RetiredByExit, domain.RetiredByRejection, domain.RetiredByAbandonment, domain.RetiredByLoss, ""} {
		require.False(t, retire(cause, start), "%q is contradicted by the execution's facts", cause)
	}
	require.False(t, retire(domain.RetiredUnreached, start.Add(time.Second)), "a later start's request is not this execution's")
	require.False(t, retire(domain.RetiredUnreached, start.Add(-time.Second)), "an earlier start's request, delivered late, is not this execution's")
	execution.RecordExit("process-1", domain.HarnessExit{})
	require.True(t, retire(domain.RetiredByExit, start))
	require.Equal(t, domain.ExecutionRetired, execution.State)
	require.Equal(t, domain.RetiredByExit, execution.Retirement)
	require.False(t, retire(domain.RetiredByExit, start), "a repeated retirement changes nothing")
	_, err := execution.Retire(domain.WorkComputeReleaseRequested{WorkID: work.ID, StartAdmittedAt: start, Cause: domain.RetiredByExit}, time.Time{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)

	for _, cause := range []domain.RetirementCause{domain.RetiredUnreached, domain.RetiredFenced, domain.RetiredWithdrawn} {
		other, launched := launchedExecution(t)
		retired, err := launched.Retire(domain.WorkComputeReleaseRequested{WorkID: other.ID, StartAdmittedAt: launched.StartAdmittedAt, Cause: cause}, time.Now())
		require.NoError(t, err)
		require.True(t, retired, "%s ends by its Work's decision", cause)
	}
}

// A retired execution deletes every environment it owns, then leaves its Desk;
// its release is confirmed only once both are done, and its Work then
// confirms its compute released.
func TestReleaseIsConfirmedOnlyWithoutEnvironmentsAndAfterTheDeskLeft(t *testing.T) {
	work, execution := launchedExecution(t)
	execution.RecordExit("process-1", domain.HarnessExit{})
	for _, r := range execution.Reports() {
		report(t, work, r)
	}
	requested, _ := intent[domain.WorkComputeReleaseRequested](work)
	retired, err := execution.Retire(requested, time.Now())
	require.NoError(t, err)
	require.True(t, retired)

	owned := domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "env-1", Running: true}, {ID: "env-2"}}}
	decision := decide(t, execution, owned, time.Now())
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-1"}, {Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-2"}}, decision.Steps, "its Desk stays until its compute is released")
	require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, time.Now())))
	confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.False(t, confirmed, "its Desk has not left")
	require.True(t, execution.RecordDeskLeft(time.Now()))
	require.False(t, execution.RecordDeskLeft(time.Now()), "its Desk leaves once")
	require.False(t, execution.HoldsDesk("desk-test"))
	confirmed, err = execution.ConfirmRelease(owned, time.Now())
	require.NoError(t, err)
	require.False(t, confirmed, "an owned environment remains")
	confirmed, err = execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.True(t, confirmed)
	require.Equal(t, domain.ExecutionReleased, execution.State)
	require.False(t, execution.ReleasedAt.IsZero())
	require.Contains(t, executionEvents(execution), domain.Event(domain.ExecutionReleasedEvent{WorkID: work.ID, StartAdmittedAt: execution.StartAdmittedAt}))

	require.Equal(t, domain.WorkFailed, work.State, "an exit before readiness fails the Work")
	for _, r := range execution.Reports() {
		report(t, work, r)
	}
	require.True(t, work.HarnessProcess.ComputeReclaimed)

	late := decide(t, execution, runningEnvironment("env-3"), time.Now())
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-3"}}, late.Steps, "an environment that appears after the release is deleted")
	require.False(t, late.Changed)
}

// A retired execution that never bound or created anything leaves its Desk
// and is released at once.
func TestARetiredExecutionWithoutEnvironmentsIsReleasedOnceItsDeskLeft(t *testing.T) {
	work := newSession(t)
	execution := beginOf(t, work, domain.RealizationCloud)
	require.True(t, execution.RecordSubmission())
	_, err := execution.RecordCreationAnswer(domain.CreationAnswer{Refused: true})
	require.NoError(t, err)
	for _, r := range execution.Reports() {
		report(t, work, r)
	}
	require.Equal(t, domain.RetiredByRejection, releaseCause(work))
	requested, _ := intent[domain.WorkComputeReleaseRequested](work)
	retired, err := execution.Retire(requested, time.Now())
	require.NoError(t, err)
	require.True(t, retired)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, time.Now())))
	require.True(t, execution.RecordDeskLeft(time.Now()))
	confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.True(t, confirmed)
}

// An execution still unretired when its Work admits a later start retires as
// superseded, whatever its facts, and then releases as any retired one; its
// own or an earlier start supersedes nothing.
func TestALaterStartSupersedesAnUnretiredExecution(t *testing.T) {
	work, execution := launchedExecution(t)
	start := execution.StartAdmittedAt
	later := domain.WorkStartAdmitted{WorkID: work.ID, StartAdmittedAt: start.Add(time.Second), DeskID: "desk-next"}
	for _, stale := range []time.Time{start, start.Add(-time.Second)} {
		superseded, err := execution.Retire(domain.WorkStartAdmitted{WorkID: work.ID, StartAdmittedAt: stale}, time.Now())
		require.NoError(t, err)
		require.False(t, superseded)
	}
	_, err := execution.Retire(domain.WorkStartAdmitted{WorkID: "other", StartAdmittedAt: later.StartAdmittedAt}, time.Now())
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	_, err = execution.Retire(domain.WorkStopRequested{WorkID: work.ID, StartAdmittedAt: start, StopRequestID: "stop"}, time.Now())
	require.ErrorIs(t, err, domain.ErrInvalidExecution)
	_, err = execution.Retire(later, time.Time{})
	require.ErrorIs(t, err, domain.ErrInvalidExecution)

	superseded, err := execution.Retire(later, time.Now())
	require.NoError(t, err)
	require.True(t, superseded)
	require.Equal(t, domain.ExecutionRetired, execution.State)
	require.Equal(t, domain.RetiredSuperseded, execution.Retirement)
	superseded, err = execution.Retire(later, time.Now())
	require.NoError(t, err)
	require.False(t, superseded, "a retired execution is not superseded again")
	require.Equal(t, []domain.ExecutionStep{{Kind: domain.StepDeleteEnvironment, EnvironmentID: "env-1"}}, decide(t, execution, runningEnvironment("env-1"), time.Now()).Steps)
	require.Equal(t, []domain.ExecutionStepKind{domain.StepLeaveDesk}, kinds(decide(t, execution, domain.EnvironmentObservation{}, time.Now())))
	require.True(t, execution.RecordDeskLeft(time.Now()))
	released, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
	require.NoError(t, err)
	require.True(t, released)
}

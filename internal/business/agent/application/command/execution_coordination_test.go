package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// The Work's admitted start begins one execution with the Work's launch,
// realized where its Computer runs it; a repeated beginning changes nothing.
func TestBeginExecutionStartsTheAdmittedStartOnce(t *testing.T) {
	for name, managed := range map[string]bool{"cloud": false, "managed": true} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			w := newWorld(t, startingSession(t))
			w.computers.Managed = managed
			if managed {
				w.work().ComputerKind = domain.ComputerManaged
			}
			execution := w.begin(t)
			require.Equal(t, w.work().ID, execution.WorkID)
			require.True(t, execution.Serves(w.work().HarnessProcess.StartingSince), "the execution serves the admitted start")
			require.Equal(t, domain.ExecutionStarting, execution.State, "a seated start's execution is seated at once")
			require.Equal(t, "desk-test", execution.Desk.ID)
			want := map[bool]domain.ExecutionRealization{false: domain.RealizationCloud, true: domain.RealizationManaged}[managed]
			require.Equal(t, want, execution.Realization)
			launch := w.work().LaunchSpecification()
			launch.ComputerName, launch.Purpose = "Fixture Computer", "test-purpose"
			require.Equal(t, execution.PreparationBound(), execution.Launch.PreparationBound, "the launch carries the seated start's preparation bound")
			launch.PreparationBound = execution.Launch.PreparationBound
			require.Equal(t, launch, execution.Launch)

			require.NoError(t, w.coordination.BeginExecution(ctx, w.work().ID))
			require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
			require.Equal(t, 1, w.executions.Count(), "one start begins one execution")
		})
	}
}

// A start waiting in its Computer's line begins a Queued execution, which is
// seated when its Desk is assigned and counts its Startup Deadline from then.
func TestAQueuedStartBeginsAQueuedExecution(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, admittedSession(t, true))
	execution := w.begin(t)
	require.True(t, execution.Queued())
	require.True(t, execution.Desk.SeatedAt.IsZero())

	seatedAt := w.now().Add(time.Minute)
	seat := command.NewSeatExecutionHandler(w.now, w.executions)
	require.ErrorIs(t, seat.DeskAssigned(ctx, w.work().ID, "another-desk", seatedAt), command.ErrDeskNotHeld)
	require.NoError(t, seat.DeskAssigned(ctx, w.work().ID, "desk-test", seatedAt))
	require.NoError(t, seat.DeskAssigned(ctx, w.work().ID, "desk-test", seatedAt.Add(time.Minute)), "a repeated assignment changes nothing")
	seated := w.execution(t)
	require.Equal(t, domain.ExecutionStarting, seated.State)
	require.Equal(t, seatedAt.Add(domain.StartupDeadline).UTC(), seated.StartupDeadlineAt().UTC())
}

// A stop of a start still waiting for its Desk withdraws it: its execution
// retires as withdrawn and launches nothing.
func TestAStopWithdrawsAQueuedStart(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, admittedSession(t, true))
	w.begin(t)
	require.NoError(t, command.NewStopWorkHandler(w.works, sessionAccess{}, w.executions).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: w.work().ID, RequestID: "stop"}))
	require.True(t, w.work().HarnessProcess.StartWithdrawn)
	require.Empty(t, w.works.stopRequests(), "a withdrawn start has no process to ask")
	require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
	require.Equal(t, domain.RetiredWithdrawn, w.execution(t).Retirement)
	require.Zero(t, w.environment.Submitted)
}

// The Work's stop is recorded on its execution once; without a stop nothing
// is recorded.
func TestRequestProcessExitRecordsTheWorksStop(t *testing.T) {
	ctx := context.Background()
	w := runningWorld(t)
	require.NoError(t, w.coordination.RequestProcessExit(ctx, w.work().ID))
	require.False(t, w.execution(t).ExitRequest.Requested(), "a running Work asks for no exit")

	w.stop(t, "stop")
	require.Equal(t, "stop", w.execution(t).ExitRequest.StopRequestID)
	saves := w.executions.Saves
	require.NoError(t, w.coordination.RequestProcessExit(ctx, w.work().ID))
	require.Equal(t, saves, w.executions.Saves, "a repeated request changes nothing")
}

// A concurrent change of the execution is retried against the execution as
// it is now, a bounded number of times.
func TestPassingAnIntentRetriesAConcurrentChange(t *testing.T) {
	for conflicts, passed := range map[int]bool{2: true, 3: false} {
		ctx := context.Background()
		w := runningWorld(t)
		require.NoError(t, w.work().Stop("stop", false))
		require.NoError(t, w.works.Save(ctx, w.work()))
		w.executions.Conflicts = conflicts
		err := w.coordination.RequestProcessExit(ctx, w.work().ID)
		if passed {
			require.NoError(t, err)
			require.Equal(t, "stop", w.execution(t).ExitRequest.StopRequestID)
			continue
		}
		require.ErrorIs(t, err, domain.ErrConcurrentModification, "the retries are bounded")
		require.False(t, w.execution(t).ExitRequest.Requested())
	}
}

// Each way a start's run ends retires its execution with that cause.
func TestRetireExecutionTakesTheCauseOfTheRunsEnd(t *testing.T) {
	for name, tc := range map[string]struct {
		end  func(t *testing.T, w *world)
		want domain.RetirementCause
	}{
		"exited": {end: func(t *testing.T, w *world) {
			w.ready(t)
			w.stop(t, "stop")
			require.NoError(t, w.feedback().Handle(context.Background(), command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: w.work().ID, ProcessID: "process"}))
		}, want: domain.RetiredByExit},
		"rejected": {end: func(t *testing.T, w *world) {
			w.begin(t)
			w.environment.Answer = domain.CreationAnswer{Refused: true}
			require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
			require.NoError(t, w.coordination.ApplyExecutionReports(context.Background(), w.work().ID))
			require.True(t, w.work().HarnessProcess.StartRejected)
		}, want: domain.RetiredByRejection},
		"lost": {end: func(t *testing.T, w *world) {
			w.ready(t)
			_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
			require.NoError(t, err)
			require.NoError(t, w.coordination.ApplyExecutionReports(context.Background(), w.work().ID))
			require.True(t, w.work().HarnessProcess.EnvironmentLost)
		}, want: domain.RetiredByLoss},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			w := newWorld(t, startingSession(t))
			tc.end(t, w)
			require.False(t, w.execution(t).Retired(), "the run's end alone retires nothing")
			require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
			require.Equal(t, tc.want, w.execution(t).Retirement)
			saves := w.executions.Saves
			require.NoError(t, w.coordination.RetireExecution(ctx, w.work().ID))
			require.Equal(t, saves, w.executions.Saves, "a repeated retirement changes nothing")
		})
	}
}

// Readiness makes the Work Working and idle only when nothing awaits
// processing in its input buffer.
func TestApplyingReadinessRecordsTheWorksIdleness(t *testing.T) {
	for name, pending := range map[string]bool{"nothing buffered": false, "input buffered": true} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, startingSession(t))
			if pending {
				w.harness.Inputs = []command.ExecutionInput{{ID: uuid.NewString(), Text: "waiting"}}
			}
			w.ready(t)
			require.Equal(t, !pending, w.work().Idle())
		})
	}
}

// A release confirmation completes the Work's stop only once the execution
// released its compute; the exit alone leaves the Work Stopping.
func TestConfirmingTheReleaseCompletesTheStop(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	w.exit(t, 0)
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.Equal(t, domain.WorkStopping, w.work().State)
	require.False(t, w.work().HarnessProcess.ComputeReclaimed)
	w.release(t)
	require.Equal(t, domain.WorkStopped, w.work().State)
	require.True(t, w.work().HarnessProcess.ComputeReclaimed)
	require.Equal(t, []string{"desk-test"}, w.computers.Left, "the Desk is left before the release is confirmed")
}

// A Domain Event lost between the two Roots is repaired by the Work's
// recovery: a missed beginning, a report the Work never applied and an exit
// request the execution never recorded are each passed by Coordinate.
func TestCoordinateRepairsLostDomainEvents(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, 1, w.executions.Count(), "the missed beginning")

	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
		prepared, err := execution.RecordPreparation(domain.PreparationReport{})
		require.NoError(t, err)
		require.True(t, prepared)
		_, err = execution.RecordLaunch("process")
		require.NoError(t, err)
		_, err = execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, w.now())
		require.NoError(t, err)
	})
	require.Equal(t, domain.WorkStarting, w.work().State)
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, domain.WorkWorking, w.work().State, "the missed ready report")

	require.NoError(t, w.work().Stop("stop", false))
	require.NoError(t, w.works.Save(ctx, w.work()))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, "stop", w.execution(t).ExitRequest.StopRequestID, "the missed exit request")
}

// Coordinate also repairs a lost exit report with the retirement it asks
// for, and a lost release report: the Work stops once its execution's
// release is applied.
func TestCoordinateRepairsLostRetirementAndRelease(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, commandtest.Submit(execution))
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
		prepared, err := execution.RecordPreparation(domain.PreparationReport{})
		require.NoError(t, err)
		require.True(t, prepared)
		_, err = execution.RecordLaunch("process")
		require.NoError(t, err)
		_, err = execution.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, w.now())
		require.NoError(t, err)
	})
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.NoError(t, w.work().Stop("stop", false))
	require.NoError(t, w.works.Save(ctx, w.work()))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))

	w.change(t, func(execution *domain.AgentExecution) {
		current, changed := execution.RecordExit("process", domain.HarnessExit{})
		require.True(t, current)
		require.True(t, changed)
	})
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, domain.ExecutionRetired, w.execution(t).State, "the missed exit report and the retirement it asks for, in one sweep")
	require.Equal(t, domain.RetiredByExit, w.execution(t).Retirement)

	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, execution.RecordDeskLeft(w.now()))
		confirmed, err := execution.ConfirmRelease(domain.EnvironmentObservation{}, w.now())
		require.NoError(t, err)
		require.True(t, confirmed)
	})
	require.Equal(t, domain.WorkStopping, w.work().State)
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, domain.WorkStopped, w.work().State, "the missed release report")
	require.True(t, w.work().HarnessProcess.ComputeReclaimed)
}

// A withdrawn Resume whose retirement request was lost leaves its Queued
// execution unretired; the next admitted start supersedes it before its own
// execution begins, and the superseded one releases as any retired one.
func TestALaterStartSupersedesAWithdrawnStartWhoseRetirementWasLost(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, stoppedSession(t))
	waiting := fixtureAdmission(w.work().AgentID)
	waiting.AdmittedAt, waiting.Seat.Waiting, waiting.Seat.DeskID = w.now().Add(-30*time.Second), true, "desk-waiting"
	require.NoError(t, w.work().Resume(waiting))
	require.NoError(t, w.works.Save(ctx, w.work()))
	withdrawn := w.begin(t)
	require.True(t, withdrawn.Queued())
	require.NoError(t, command.NewStopWorkHandler(w.works, sessionAccess{}, w.executions).Handle(ctx, command.WorkControl{SubjectID: "owner", WorkID: w.work().ID, RequestID: "stop-waiting"}))
	require.True(t, w.work().HarnessProcess.StartWithdrawn)
	require.Equal(t, domain.WorkStopped, w.work().State)
	// The retirement request is lost: the queued execution stays unretired.
	require.False(t, w.execution(t).Retired())

	require.NoError(t, w.work().Resume(fixtureAdmissionAt(w.work().AgentID, w.now())))
	require.NoError(t, w.works.Save(ctx, w.work()))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	superseded, err := w.executions.Get(ctx, withdrawn.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RetiredSuperseded, superseded.Retirement, "the earlier execution retires first")
	current := w.execution(t)
	require.NotEqual(t, withdrawn.ID, current.ID, "the admitted start begins its own execution")
	require.True(t, current.Serves(w.work().HarnessProcess.StartingSince))
	require.Equal(t, domain.ExecutionStarting, current.State)

	require.True(t, superseded.RecordDeskLeft(w.now()))
	released, err := superseded.ConfirmRelease(domain.EnvironmentObservation{}, w.now())
	require.NoError(t, err)
	require.True(t, released)
	require.NoError(t, w.executions.Save(ctx, superseded))
	require.NoError(t, w.coordination.Coordinate(ctx, w.work().ID))
	require.Equal(t, domain.WorkStarting, w.work().State, "the superseded release reports nothing about the current start")
}

// fixtureAdmissionAt is a seated admission at the given time.
// fixtureAdmissionAt admits a later start of the Work at the time.
func fixtureAdmissionAt(agentID string, at time.Time) domain.StartAdmission {
	admission := nextAdmission(agentID)
	admission.AdmittedAt = at
	return admission
}

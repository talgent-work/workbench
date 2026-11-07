package command_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// running observes the named environments running.
func running(ids ...string) domain.EnvironmentObservation {
	var observation domain.EnvironmentObservation
	for _, id := range ids {
		observation.Environments = append(observation.Environments, domain.ObservedEnvironment{ID: id, Running: true})
	}
	return observation
}

// boundWorld is a Work whose execution bound "environment" and delivered its
// credentials, nothing launched yet.
func boundWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, startingSession(t))
	w.begin(t)
	w.change(t, func(execution *domain.AgentExecution) {
		require.True(t, execution.RecordSubmission())
		_, err := execution.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: "environment"})
		require.NoError(t, err)
		require.True(t, execution.RecordCredentialDelivery(domain.CredentialDelivery{}))
	})
	return w
}

// The submission is recorded before the creation is submitted, and the
// creation is submitted once; its answer is decided on at once.
func TestCreationIsSubmittedOnceAfterItsSubmissionIsRecorded(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	execution := w.begin(t)
	w.environment.Answer = domain.CreationAnswer{EnvironmentID: "environment"}
	w.environment.Observed = func(execution *domain.AgentExecution) domain.EnvironmentObservation {
		if execution.Creation.Submitted {
			return running("environment")
		}
		return domain.EnvironmentObservation{}
	}
	long, err := w.reconciliation().Reconcile(ctx, execution.ID)
	require.NoError(t, err)
	require.Equal(t, 1, w.environment.Submitted)
	require.True(t, w.environment.SubmittedState[0].Creation.Submitted, "the submission is durable before the creation is sent")
	require.Equal(t, "environment", w.execution(t).Environment)
	require.Equal(t, 1, w.environment.Delivered, "the created environment is decided on at once")
	require.True(t, w.execution(t).Credentials.Delivered)
	require.True(t, long, "the delivered environment's preparation is due")
	require.Zero(t, w.environment.Prepared, "a reconciliation never takes a long step")

	_, err = w.reconciliation().Reconcile(ctx, execution.ID)
	require.NoError(t, err)
	require.Equal(t, 1, w.environment.Submitted, "a creation is never submitted twice")
}

// A refused creation rejects the start; nothing more is submitted.
func TestARefusedCreationRejectsTheStart(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t, startingSession(t))
	execution := w.begin(t)
	w.environment.Answer = domain.CreationAnswer{Refused: true}
	_, err := w.reconciliation().Reconcile(ctx, execution.ID)
	require.NoError(t, err)
	require.True(t, w.execution(t).Outcome.Rejected)
	require.NoError(t, w.coordination.ApplyExecutionReports(ctx, w.work().ID))
	require.True(t, w.work().HarnessProcess.StartRejected)
	require.False(t, w.work().HoldsActiveProcess())
	_, err = w.reconciliation().Reconcile(ctx, execution.ID)
	require.NoError(t, err)
	require.Equal(t, 1, w.environment.Submitted)
}

// A rejected first-start preparation refuses the creation without
// submitting it; a pending one holds it back.
func TestTheFirstStartsPreparationGatesTheCreation(t *testing.T) {
	ctx := context.Background()
	for name, request := range map[string]command.InitialExecutionRequest{
		"pending":  {Submitted: true},
		"rejected": {Submitted: true, Rejected: true},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, startingSession(t))
			execution := w.begin(t)
			request.WorkID = w.work().ID
			initial := &initialRequestStore{request: request}
			reconcile := command.NewReconcileExecutionHandler(w.now, w.executions, w.environment, w.harness, w.computers, noMounts{}, initial)
			_, err := reconcile.Reconcile(ctx, execution.ID)
			require.NoError(t, err)
			require.Zero(t, w.environment.Submitted)
			require.Equal(t, name == "rejected", w.execution(t).Outcome.Rejected)
		})
	}
}

// Every environment carrying the execution's ownership other than the bound
// one is deleted.
func TestSurplusEnvironmentsAreDeleted(t *testing.T) {
	w := boundWorld(t)
	w.environment.Observation = running("surplus", "environment")
	_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, []string{"surplus"}, w.environment.Deleted)
	require.Equal(t, "environment", w.execution(t).Environment)
}

// A retired execution deletes what it owns, leaves its Desk only once no
// environment remains, and only then confirms its release.
func TestReleaseIsConfirmedOnlyAfterTheDeskIsLeftAndNothingRemains(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	w.exit(t, 0)
	require.True(t, w.execution(t).Retired())

	w.environment.Observation = running("environment")
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Contains(t, w.environment.Deleted, "environment")
	require.Empty(t, w.computers.Left, "the Desk is held while an environment remains")
	require.Equal(t, domain.ExecutionRetired, w.execution(t).State)

	w.environment.Observation = domain.EnvironmentObservation{}
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, []string{"desk-test"}, w.computers.Left)
	require.Equal(t, domain.ExecutionReleased, w.execution(t).State)
}

// A deletion that takes effect at once is followed in the same
// reconciliation by the Desk's departure and the release.
func TestADeletionIsFollowedByTheReleaseInOneReconciliation(t *testing.T) {
	ctx := context.Background()
	w := stoppingWorld(t)
	w.exit(t, 0)
	w.environment.Observed = func(*domain.AgentExecution) domain.EnvironmentObservation {
		if len(w.environment.Deleted) > 0 {
			return domain.EnvironmentObservation{}
		}
		return running("environment")
	}
	_, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, []string{"environment"}, w.environment.Deleted)
	require.Equal(t, []string{"desk-test"}, w.computers.Left)
	require.Equal(t, domain.ExecutionReleased, w.execution(t).State)
}

// A Queued execution whose Desk Assigned was missed is seated from what
// Computer holds.
func TestSeatingIsRecoveredFromComputer(t *testing.T) {
	w := newWorld(t, admittedSession(t, true))
	execution := w.begin(t)
	_, err := w.reconciliation().Reconcile(context.Background(), execution.ID)
	require.NoError(t, err)
	require.True(t, w.execution(t).Queued(), "an unseated Desk keeps the execution Queued")
	require.Zero(t, w.environment.Submitted)

	seatedAt := w.now().Add(-time.Minute)
	w.computers.Seat("desk-test", seatedAt)
	w.environment.Answer = domain.CreationAnswer{EnvironmentID: "environment"}
	w.environment.Observation = running("environment")
	_, err = w.reconciliation().Reconcile(context.Background(), execution.ID)
	require.NoError(t, err)
	seated := w.execution(t)
	require.Equal(t, domain.ExecutionStarting, seated.State)
	require.Equal(t, seatedAt.Add(domain.StartupDeadline).UTC(), seated.StartupDeadlineAt().UTC())
	require.Equal(t, 1, w.environment.Submitted, "the seated start submits its creation")
}

// An exit request withdraws the delivered credentials and asks the launched
// process to exit, cancelling a Waiting Turn first when the Work asked.
func TestAnExitRequestWithdrawsCredentialsAndSignalsTheExit(t *testing.T) {
	for name, cancel := range map[string]bool{"stop": false, "waiting timeout": true} {
		t.Run(name, func(t *testing.T) {
			w := runningWorld(t)
			w.harness.Turn = command.CurrentTurn{ID: "turn", Active: true}
			w.change(t, func(execution *domain.AgentExecution) {
				_, err := execution.RequestExit(domain.WorkStopRequested{WorkID: w.work().ID, StartAdmittedAt: w.work().HarnessProcess.StartingSince, StopRequestID: "stop", CancelWaitingTurn: cancel})
				require.NoError(t, err)
			})
			w.environment.Observation = running("environment")
			w.environment.Observation.Attached = true
			_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
			require.NoError(t, err)
			require.Equal(t, 1, w.environment.Withdrawn)
			require.True(t, w.execution(t).Credentials.Withdrawn)
			require.Equal(t, []string{"stop"}, w.harness.Stops)
			if cancel {
				require.Equal(t, []string{"turn"}, w.harness.Cancels)
			} else {
				require.Empty(t, w.harness.Cancels)
			}
		})
	}
}

// Preparation is bounded by the Startup Deadline: one still running at the
// deadline launches nothing, and a start past it does not prepare.
func TestPreparationIsBoundedByTheStartupDeadline(t *testing.T) {
	ctx := context.Background()
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	w.change(t, func(execution *domain.AgentExecution) {
		execution.Desk.SeatedAt = time.Now().Add(-domain.StartupDeadline + 100*time.Millisecond)
	})
	w.at = time.Now()
	w.environment.PrepareFunc = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Equal(t, 1, w.environment.Prepared)
	require.Zero(t, w.environment.Launched, "no Harness is launched past the deadline")
	require.False(t, w.execution(t).Process.PreparationEnded)

	w.at = w.execution(t).StartupDeadlineAt()
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	require.Equal(t, 1, w.environment.Prepared, "a start past its deadline does not prepare")
	require.Zero(t, w.environment.Launched)
}

// A prepared start launches its Harness and connects it, each outcome
// recorded before the next.
func TestPreparationLaunchesAndConnectsTheHarness(t *testing.T) {
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	execution := w.execution(t)
	require.True(t, execution.Process.PreparationEnded)
	require.Equal(t, "process", execution.Process.ID)
	require.Equal(t, 1, w.environment.Launched)
	require.Equal(t, 1, w.environment.Connected)
}

// A step's outcome lost to a concurrent change of the execution is applied
// again to the execution as it is then.
func TestAStepOutcomeIsRecordedDespiteAConcurrentChange(t *testing.T) {
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	w.environment.PrepareFunc = func(context.Context) error {
		w.executions.Conflicts = 2
		return nil
	}
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	require.True(t, w.execution(t).Process.PreparationEnded, "the preparation is recorded on the third attempt")
}

// A reconciliation takes the short steps of a launched process itself: it
// reconnects a process Dispatcher does not read once, however many decisions
// it makes, and replaces credentials marked changed.
func TestReconciliationReconnectsAndReplacesCredentialsItself(t *testing.T) {
	ctx := context.Background()
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	require.NoError(t, w.reconciliation().RunLongStep(ctx, w.execution(t).ID))
	connected := w.environment.Connected
	long, err := w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.False(t, long)
	require.Equal(t, connected+1, w.environment.Connected, "a process Dispatcher does not read is reconnected once")

	w.environment.Observation.Attached = true
	w.change(t, func(execution *domain.AgentExecution) { require.True(t, execution.MarkCredentialsChanged()) })
	delivered := w.environment.Delivered
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, delivered+1, w.environment.Delivered, "changed credentials are replaced")
	_, err = w.reconciliation().Reconcile(ctx, w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, delivered+1, w.environment.Delivered, "the replacement is recorded")
}

// A Managed execution's changed inputs are synced by its reconciliation.
func TestReconciliationSyncsChangedInputs(t *testing.T) {
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	w.change(t, func(execution *domain.AgentExecution) {
		execution.Realization = domain.RealizationManaged
		require.True(t, execution.MarkInputsChanged())
	})
	_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
	require.NoError(t, err)
	require.Equal(t, 1, w.environment.Synced)
	require.Zero(t, w.execution(t).Inputs.Due, "the sync is recorded")
}

// A long step no longer decided is not taken.
func TestALongStepNoLongerDecidedIsNotTaken(t *testing.T) {
	w := boundWorld(t)
	w.environment.Observation = running("environment")
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	require.NoError(t, w.reconciliation().RunLongStep(context.Background(), w.execution(t).ID))
	require.Equal(t, 1, w.environment.Prepared, "a launched execution prepares nothing more")
}

// A lease near its expiry is renewed in one reconciliation, and the expiry
// the authority returned is recorded, so the next reconciliation leaves it.
func TestALeaseNearExpiryIsRenewedAndItsExpiryRecorded(t *testing.T) {
	w := runningWorld(t)
	terms := domain.LeaseTerms{Duration: 2 * time.Hour, RenewalThreshold: time.Hour}
	expiry := w.at.Add(30 * time.Minute)
	w.environment.Observation = domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: "environment", Running: true, LeaseExpiresAt: expiry}}, Attached: true, Lease: terms}
	_, err := w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
	require.NoError(t, err)
	renewed := w.at.Add(2 * time.Hour).UTC()
	require.Len(t, w.environment.Renewals, 1)
	require.True(t, renewed.Equal(w.environment.Renewals[0]))
	require.True(t, renewed.Truncate(time.Millisecond).Equal(w.execution(t).Lease), "the returned expiry is the lease")

	_, err = w.reconciliation().Reconcile(context.Background(), w.execution(t).ID)
	require.NoError(t, err)
	require.Len(t, w.environment.Renewals, 1, "a renewed lease is left alone")
}

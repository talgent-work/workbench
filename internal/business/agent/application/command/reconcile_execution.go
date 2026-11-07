package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ReconcileExecutionHandler fulfils the steps each AgentExecution decides:
// it reads the Desk's seating, observes the execution's environments, lets
// the execution decide, and takes each decided step through the port that
// reaches it, recording the outcome on the execution.
type ReconcileExecutionHandler struct {
	now          func() time.Time
	executions   domain.AgentExecutionRepository
	environments ExecutionEnvironmentPort
	harness      RunningHarnessPort
	computers    ComputerPort
	mounts       SandboxMountPort
	initial      InitialExecutionReader
}

func NewReconcileExecutionHandler(now func() time.Time, executions domain.AgentExecutionRepository, environments ExecutionEnvironmentPort, harness RunningHarnessPort, computers ComputerPort, mounts SandboxMountPort, initial InitialExecutionReader) *ReconcileExecutionHandler {
	return &ReconcileExecutionHandler{now: now, executions: executions, environments: environments, harness: harness, computers: computers, mounts: mounts, initial: initial}
}

// separateSteps are taken by their own tasks: preparation waits minutes, and
// a delivery, connection or input sync that fails is retried apart.
var separateSteps = []domain.ExecutionStepKind{domain.StepPrepare, domain.StepDeliverCredentials, domain.StepConnect, domain.StepSyncInputs}

// reconcileRounds bounds the decisions one reconciliation makes: a submitted
// creation's answer, and a retired execution's environments after their
// deletion, are decided on at once, not on the next wake.
const reconcileRounds = 2

// Reconcile decides the execution's steps and takes the short ones: its
// creation's submission, environment deletion, credential withdrawal, the
// exit signal and its Desk's departure, then confirms a retired execution's
// release. It returns the decided steps taken apart. A full reconciliation
// lists the environments carrying the execution's ownership even when
// Dispatcher reads its process.
func (h *ReconcileExecutionHandler) Reconcile(ctx context.Context, executionID string, full bool) ([]domain.ExecutionStepKind, error) {
	var separate []domain.ExecutionStepKind
	for range reconcileRounds {
		steps, again, err := h.round(ctx, executionID, full)
		separate = steps
		if err != nil || !again {
			return separate, err
		}
	}
	return separate, nil
}

// round makes one decision and takes its short steps; it reports whether
// another round follows at once: after the execution's creation was
// submitted, or after a retired execution deleted environments, so it leaves
// its Desk once none remains.
func (h *ReconcileExecutionHandler) round(ctx context.Context, executionID string, full bool) ([]domain.ExecutionStepKind, bool, error) {
	execution, decision, observation, err := h.decide(ctx, executionID, full)
	if err != nil || execution == nil {
		return nil, false, err
	}
	var separate []domain.ExecutionStepKind
	var failures []error
	deleted, submitted := false, false
	for _, step := range decision.Steps {
		switch step.Kind {
		case domain.StepSubmitCreation:
			err := h.submit(ctx, execution)
			submitted = err == nil
			failures = append(failures, err)
		case domain.StepDeleteEnvironment:
			err := h.environments.DeleteEnvironment(ctx, execution, step.EnvironmentID)
			deleted = deleted || err == nil
			failures = append(failures, err)
		case domain.StepWithdrawCredentials:
			failures = append(failures, h.withdraw(ctx, execution))
		case domain.StepSignalExit:
			failures = append(failures, h.signalExit(ctx, execution))
		case domain.StepLeaveDesk:
			failures = append(failures, h.leaveDesk(ctx, execution))
		default:
			separate = append(separate, step.Kind)
		}
	}
	if err := errors.Join(failures...); err != nil {
		return separate, false, err
	}
	if execution.Retired() {
		return separate, deleted, h.confirmRelease(ctx, executionID, observation, deleted)
	}
	return separate, submitted, nil
}

// decide loads the execution, applies its Desk's seating while Queued,
// observes it and lets it decide; a change is saved before any step is taken.
func (h *ReconcileExecutionHandler) decide(ctx context.Context, executionID string, full bool) (*domain.AgentExecution, domain.ExecutionDecision, domain.EnvironmentObservation, error) {
	var none domain.ExecutionDecision
	var observation domain.EnvironmentObservation
	execution, err := h.executions.Get(ctx, executionID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil, none, observation, nil
	}
	if err != nil {
		return nil, none, observation, err
	}
	if execution.State == domain.ExecutionReleased && !full {
		return nil, none, observation, nil
	}
	seated, err := h.seat(ctx, execution)
	if err != nil {
		return nil, none, observation, err
	}
	rejected, err := h.prerequisite(ctx, execution, &observation)
	if err != nil {
		return nil, none, observation, err
	}
	held := observation.CreationHeld
	if observation, err = h.environments.Observe(ctx, execution, full || execution.Retired()); err != nil {
		return nil, none, observation, err
	}
	observation.CreationHeld = held
	decision, err := execution.Reconcile(observation, h.now())
	if err != nil {
		return nil, none, observation, err
	}
	if seated || rejected || decision.Changed {
		if err := h.executions.Save(ctx, execution); err != nil {
			return nil, none, observation, err
		}
		if execution, err = h.executions.Get(ctx, executionID); err != nil {
			return nil, none, observation, err
		}
	}
	return execution, decision, observation, nil
}

// seat reads from Computer whether a Queued execution's Desk is seated, the
// recovery of a Desk Assigned it missed (Seat execution).
func (h *ReconcileExecutionHandler) seat(ctx context.Context, execution *domain.AgentExecution) (bool, error) {
	if !execution.Queued() {
		return false, nil
	}
	seatedAt, seated, err := h.computers.DeskSeated(ctx, execution.Desk.ID)
	if err != nil || !seated {
		return false, err
	}
	if seatedAt.IsZero() {
		seatedAt = h.now()
	}
	return execution.Seat(seatedAt)
}

// prerequisite applies the Work's first-start preparation to an execution
// that has not submitted its creation: a pending one holds the creation back,
// and a rejected one refuses it.
func (h *ReconcileExecutionHandler) prerequisite(ctx context.Context, execution *domain.AgentExecution, observation *domain.EnvironmentObservation) (bool, error) {
	if execution.Creation.Submitted || execution.Retired() || h.initial == nil {
		return false, nil
	}
	initial, err := h.initial.Get(ctx, execution.WorkID)
	if errors.Is(err, ErrInitialExecutionNotFound) {
		return false, nil
	}
	if err != nil || initial.Prepared {
		return false, err
	}
	if !initial.Rejected {
		observation.CreationHeld = true
		return false, nil
	}
	return execution.RecordCreationAnswer(domain.CreationAnswer{Refused: true})
}

// submit fixes the environment's specification with the Mount Grants
// Storage issues on a Cloud, records the submission, and only then submits it
// once; a lost answer leaves observation and the Startup Deadline to decide.
func (h *ReconcileExecutionHandler) submit(ctx context.Context, execution *domain.AgentExecution) error {
	var mounts domain.SandboxMounts
	if execution.Realization == domain.RealizationCloud {
		var err error
		if mounts, err = h.mounts.AcquireMounts(ctx, execution.Launch.MountRequest()); err != nil {
			return err
		}
	}
	if err := h.environments.PrepareCreation(ctx, execution, mounts); err != nil {
		return err
	}
	if !execution.RecordSubmission() {
		return nil
	}
	if err := h.executions.Save(ctx, execution); err != nil {
		return err
	}
	submitted, err := h.executions.Get(ctx, execution.ID)
	if err != nil {
		return err
	}
	answer, err := h.environments.SubmitCreation(ctx, submitted)
	if err != nil {
		return err
	}
	return h.record(ctx, submitted.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordCreationAnswer(answer)
	})
}

func (h *ReconcileExecutionHandler) withdraw(ctx context.Context, execution *domain.AgentExecution) error {
	if err := h.environments.WithdrawCredentials(ctx, execution); err != nil {
		return err
	}
	return h.record(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordCredentialsWithdrawn(), nil
	})
}

// signalExit asks the launched process to exit under the Work's stop
// identity, cancelling a Waiting Turn first when the Work asked (Timeout
// cancels first).
func (h *ReconcileExecutionHandler) signalExit(ctx context.Context, execution *domain.AgentExecution) error {
	if execution.ExitRequest.CancelWaitingTurn {
		if err := cancelRunningTurn(ctx, h.harness, execution.WorkID); err != nil {
			return err
		}
	}
	return h.harness.StopExecution(ctx, execution.WorkID, execution.ExitRequest.StopRequestID)
}

func (h *ReconcileExecutionHandler) leaveDesk(ctx context.Context, execution *domain.AgentExecution) error {
	if execution.Desk.ID != "" {
		if err := h.computers.LeaveDesk(ctx, execution.Desk.ID); err != nil {
			return err
		}
	}
	return h.record(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordDeskLeft(h.now()), nil
	})
}

// confirmRelease confirms a retired execution's release once it left its
// Desk and owns no environment, observed again after a deletion.
func (h *ReconcileExecutionHandler) confirmRelease(ctx context.Context, executionID string, observation domain.EnvironmentObservation, deleted bool) error {
	execution, err := h.executions.Get(ctx, executionID)
	if err != nil || execution.State != domain.ExecutionRetired || execution.Desk.LeftAt.IsZero() {
		return err
	}
	if deleted {
		if observation, err = h.environments.Observe(ctx, execution, true); err != nil {
			return err
		}
	}
	confirmed, err := execution.ConfirmRelease(observation, h.now())
	if err != nil || !confirmed {
		return err
	}
	return h.executions.Save(ctx, execution)
}

// recordAttempts bounds the reloads of an outcome whose save lost a
// concurrent change of the execution.
const recordAttempts = 3

// record applies one step's outcome to the execution as it is now and saves
// it; a concurrent change makes it apply the outcome again to the execution
// as it is then.
func (h *ReconcileExecutionHandler) record(ctx context.Context, executionID string, apply func(*domain.AgentExecution) (bool, error)) error {
	var err error
	for range recordAttempts {
		var execution *domain.AgentExecution
		if execution, err = h.executions.Get(ctx, executionID); err != nil {
			return err
		}
		changed, applyErr := apply(execution)
		if applyErr != nil || !changed {
			return applyErr
		}
		if err = h.executions.Save(ctx, execution); !errors.Is(err, domain.ErrConcurrentModification) {
			return err
		}
	}
	return err
}

// cancelRunningTurn ends the Turn the Harness is running, if one is; asking
// again while its cancellation is pending, or after it ended, changes
// nothing.
func cancelRunningTurn(ctx context.Context, harness RunningHarnessPort, workID string) error {
	turn, err := harness.GetCurrentTurn(ctx, workID)
	if err != nil || !turn.Active || turn.ID == "" || turn.CancelRequested {
		return err
	}
	err = harness.CancelTurn(ctx, workID, turn.ID)
	if errors.Is(err, domain.ErrWorkTransition) {
		// The Turn ended before the cancellation reached it.
		return nil
	}
	return err
}

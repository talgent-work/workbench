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
}

func NewReconcileExecutionHandler(now func() time.Time, executions domain.AgentExecutionRepository, environments ExecutionEnvironmentPort, harness RunningHarnessPort, computers ComputerPort, mounts SandboxMountPort) *ReconcileExecutionHandler {
	return &ReconcileExecutionHandler{now: now, executions: executions, environments: environments, harness: harness, computers: computers, mounts: mounts}
}

// reconcileRounds bounds the decisions one reconciliation makes: after a step
// that changed the execution, such as a submitted creation, a delivery, or a
// retired execution's deleted environments, the next step is decided at once,
// not on the next wake, which the running reconciliation would absorb.
const reconcileRounds = 4

// Reconcile decides the execution's steps and takes each short one: its
// creation's submission, environment deletion, credential delivery and
// withdrawal, the connection of its launched process, input sync, the exit
// signal and its Desk's departure, then confirms a retired execution's
// release. It reports whether the execution's last decision called for a
// long step, which RunLongStep takes apart. Every decision observes the
// environments carrying the execution's ownership as listed now, so a wake
// absorbed by a pending reconciliation is observed all the same.
func (h *ReconcileExecutionHandler) Reconcile(ctx context.Context, executionID string) (bool, error) {
	// A step that changed nothing, such as a deletion or a connection, would
	// be decided again in the next round; it is taken once per
	// reconciliation.
	unchanged := map[domain.ExecutionStep]bool{}
	long := false
	for range reconcileRounds {
		outcome, err := h.round(ctx, executionID, unchanged)
		long = outcome.longStepDue
		if err != nil || !outcome.again {
			return long, err
		}
	}
	return long, nil
}

// roundOutcome is what one decision led to: whether it called for a long
// step, and whether another decision follows at once.
type roundOutcome struct {
	longStepDue, again bool
}

// round makes one decision and takes its short steps; another round follows
// at once after a step that changed the execution, or after a retired
// execution deleted environments, so it leaves its Desk once none remains.
func (h *ReconcileExecutionHandler) round(ctx context.Context, executionID string, unchanged map[domain.ExecutionStep]bool) (roundOutcome, error) {
	var outcome roundOutcome
	execution, decision, observation, err := h.decide(ctx, executionID)
	if err != nil || execution == nil {
		return outcome, err
	}
	var failures []error
	deleted, progressed := false, false
	for _, step := range decision.Steps {
		if longStep(step.Kind) {
			outcome.longStepDue = true
			continue
		}
		if unchanged[step] {
			continue
		}
		changed, err := h.takeShortStep(ctx, execution, step)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		deleted = deleted || step.Kind == domain.StepDeleteEnvironment
		progressed = progressed || changed
		unchanged[step] = !changed
	}
	if err := errors.Join(failures...); err != nil {
		return outcome, err
	}
	if execution.Retired() {
		outcome.again = deleted || progressed
		return outcome, h.confirmRelease(ctx, executionID, observation, deleted)
	}
	outcome.again = progressed
	return outcome, nil
}

// decide loads the execution, applies its Desk's seating while Queued,
// observes it and lets it decide; a change is saved before any step is taken.
// A released execution is observed too: a sandbox appearing after its release
// carries its ownership and is deleted.
func (h *ReconcileExecutionHandler) decide(ctx context.Context, executionID string) (*domain.AgentExecution, domain.ExecutionDecision, domain.EnvironmentObservation, error) {
	var none domain.ExecutionDecision
	var observation domain.EnvironmentObservation
	execution, err := h.executions.Get(ctx, executionID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil, none, observation, nil
	}
	if err != nil {
		return nil, none, observation, err
	}
	seated, err := h.seat(ctx, execution)
	if err != nil {
		return nil, none, observation, err
	}
	if observation, err = h.environments.Observe(ctx, execution); err != nil {
		return nil, none, observation, err
	}
	decision, err := execution.Reconcile(observation, h.now())
	if err != nil {
		return nil, none, observation, err
	}
	if seated || decision.Changed {
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

// confirmRelease confirms a retired execution's release once it left its
// Desk and owns no environment, observed again after a deletion.
func (h *ReconcileExecutionHandler) confirmRelease(ctx context.Context, executionID string, observation domain.EnvironmentObservation, deleted bool) error {
	execution, err := h.executions.Get(ctx, executionID)
	if err != nil || execution.State != domain.ExecutionRetired || execution.Desk.LeftAt.IsZero() {
		return err
	}
	if deleted {
		if observation, err = h.environments.Observe(ctx, execution); err != nil {
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
	_, err := h.recorded(ctx, executionID, apply)
	return err
}

// recorded records like record and reports whether the outcome changed the
// execution.
func (h *ReconcileExecutionHandler) recorded(ctx context.Context, executionID string, apply func(*domain.AgentExecution) (bool, error)) (bool, error) {
	var err error
	for range recordAttempts {
		var execution *domain.AgentExecution
		if execution, err = h.executions.Get(ctx, executionID); err != nil {
			return false, err
		}
		changed, applyErr := apply(execution)
		if applyErr != nil || !changed {
			return false, applyErr
		}
		if err = h.executions.Save(ctx, execution); !errors.Is(err, domain.ErrConcurrentModification) {
			return err == nil, err
		}
	}
	return false, err
}

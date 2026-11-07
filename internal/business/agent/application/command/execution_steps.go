package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// launchTimeout bounds starting the Harness and connecting it.
const launchTimeout = 30 * time.Second

// DeliverCredentials delivers or replaces the bound environment's
// credentials when the execution still decides so, and records what was
// delivered.
func (h *ReconcileExecutionHandler) DeliverCredentials(ctx context.Context, executionID string) error {
	execution, decision, _, err := h.decide(ctx, executionID, false)
	if err != nil || execution == nil || !decision.Includes(domain.StepDeliverCredentials) {
		return err
	}
	delivery, err := h.environments.DeliverCredentials(ctx, execution)
	if err != nil {
		return err
	}
	return h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordCredentialDelivery(delivery), nil
	})
}

// Connect hands the launched process to Dispatcher again when the execution
// still decides so.
func (h *ReconcileExecutionHandler) Connect(ctx context.Context, executionID string) error {
	execution, decision, _, err := h.decide(ctx, executionID, false)
	if err != nil || execution == nil || !decision.Includes(domain.StepConnect) {
		return err
	}
	return h.environments.Connect(ctx, execution)
}

// SyncInputs syncs a Managed Computer's input copies when the execution
// still decides so, and records the changes it covered.
func (h *ReconcileExecutionHandler) SyncInputs(ctx context.Context, executionID string) error {
	execution, decision, _, err := h.decide(ctx, executionID, false)
	if err != nil || execution == nil || !decision.Includes(domain.StepSyncInputs) {
		return err
	}
	synced, err := h.environments.SyncInputs(ctx, execution)
	if err != nil {
		return err
	}
	return h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordInputSync(synced, h.now()), nil
	})
}

// Prepare sees the in-environment preparation through to its exit, no later
// than the Startup Deadline, then launches the Harness and connects it, each
// outcome recorded before the next. A start past its deadline launches
// nothing; the execution records its failure.
func (h *ReconcileExecutionHandler) Prepare(ctx context.Context, executionID string) error {
	execution, decision, _, err := h.decide(ctx, executionID, false)
	if err != nil || execution == nil || !decision.Includes(domain.StepPrepare) {
		return err
	}
	deadline := execution.StartupDeadlineAt()
	if !h.now().Before(deadline) {
		return nil
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if !execution.Process.PreparationEnded {
		err := h.environments.Prepare(bounded, execution)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// The Startup Deadline passed while it prepared; the execution's
			// next reconciliation records the failure.
			return nil
		}
		if err != nil {
			return err
		}
		if err := h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
			return execution.RecordPreparation(), nil
		}); err != nil {
			return err
		}
		if execution, err = h.executions.Get(ctx, executionID); err != nil {
			return err
		}
	}
	launch, release := context.WithTimeout(ctx, launchTimeout)
	defer release()
	processID, err := h.environments.Launch(launch, execution)
	if err != nil {
		return err
	}
	launched := false
	if err := h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
		changed, err := execution.RecordLaunch(processID)
		launched = changed
		return changed, err
	}); err != nil || !launched {
		// An uncertain or refused record leaves the launched process to its
		// environment's deletion; it is never connected.
		return err
	}
	if execution, err = h.executions.Get(ctx, executionID); err != nil {
		return err
	}
	return h.environments.Connect(launch, execution)
}

// MarkCredentialsChanged records, on every unretired execution whose Work
// uses the Provider Authorization, that its delivered credentials are due
// (Credential replacement), and lists the ones marked. One that cannot be
// marked now does not hold back the others; the failures are returned.
func (h *ReconcileExecutionHandler) MarkCredentialsChanged(ctx context.Context, ids []string) ([]string, error) {
	return h.markEach(ctx, ids, (*domain.AgentExecution).MarkCredentialsChanged)
}

// MarkInputsChanged records, on every listed Managed execution, an input
// change its machine has not received (Sync changed inputs), and lists the
// ones marked.
func (h *ReconcileExecutionHandler) MarkInputsChanged(ctx context.Context, ids []string) ([]string, error) {
	return h.markEach(ctx, ids, (*domain.AgentExecution).MarkInputsChanged)
}

func (h *ReconcileExecutionHandler) markEach(ctx context.Context, ids []string, mark func(*domain.AgentExecution) bool) ([]string, error) {
	var marked []string
	var failures []error
	for _, id := range ids {
		var changed bool
		err := h.record(ctx, id, func(execution *domain.AgentExecution) (bool, error) {
			changed = mark(execution)
			return changed, nil
		})
		failures = append(failures, err)
		if err == nil && changed {
			marked = append(marked, id)
		}
	}
	return marked, errors.Join(failures...)
}

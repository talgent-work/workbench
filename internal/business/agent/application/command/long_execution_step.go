package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// longStep reports a step that may wait minutes. Such a step is taken by
// RunLongStep on its own workers, so it never delays the short steps of the
// same or any other execution.
func longStep(kind domain.ExecutionStepKind) bool {
	return kind == domain.StepPrepare
}

// launchTimeout bounds starting the Harness and connecting it.
const launchTimeout = 30 * time.Second

// RunLongStep takes the long step the execution's current facts call for, if
// any; a step no longer decided is not taken.
func (h *ReconcileExecutionHandler) RunLongStep(ctx context.Context, executionID string) error {
	execution, decision, _, err := h.decide(ctx, executionID)
	if err != nil || execution == nil {
		return err
	}
	for _, step := range decision.Steps {
		switch step.Kind {
		case domain.StepPrepare:
			return h.prepare(ctx, execution)
		}
	}
	return nil
}

// prepare sees the in-environment preparation (Plugins, then the repositories
// the run checks out) through to its exit, no later than the Startup
// Deadline, recording its Plugins' installation and its checkout outcomes
// with its end, then launches the Harness and connects it, each outcome
// recorded before the next, so the Harness starts only once every remaining
// repository has an outcome. A start past its deadline launches
// nothing; the execution records its failure.
func (h *ReconcileExecutionHandler) prepare(ctx context.Context, execution *domain.AgentExecution) error {
	executionID := execution.ID
	deadline := execution.StartupDeadlineAt()
	if !h.now().Before(deadline) {
		return nil
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	if !execution.Process.PreparationEnded {
		report, err := h.environments.Prepare(bounded, execution)
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// The Startup Deadline passed while it prepared; the execution's
			// next reconciliation records the failure.
			return nil
		}
		if err != nil {
			return err
		}
		if err := h.record(ctx, executionID, func(execution *domain.AgentExecution) (bool, error) {
			return execution.RecordPreparation(report)
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

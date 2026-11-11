package command

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// takeShortStep takes one short step the execution decided and reports
// whether it changed the execution. A step that changed nothing, such as a
// deletion the authority may still show or a connection, would be decided
// again unchanged.
func (h *ReconcileExecutionHandler) takeShortStep(ctx context.Context, execution *domain.AgentExecution, step domain.ExecutionStep) (bool, error) {
	switch step.Kind {
	case domain.StepDeleteEnvironment:
		return false, h.environments.DeleteEnvironment(ctx, execution, step.EnvironmentID)
	case domain.StepDeliverCredentials:
		return h.deliverCredentials(ctx, execution)
	case domain.StepWithdrawCredentials:
		return h.withdraw(ctx, execution)
	case domain.StepSyncInputs:
		return h.syncInputs(ctx, execution)
	case domain.StepLeaveDesk:
		return h.leaveDesk(ctx, execution)
	case domain.StepRenewLease:
		return h.renewLease(ctx, execution, step)
	case domain.StepConnect:
		return false, h.environments.Connect(ctx, execution)
	case domain.StepSignalExit:
		return false, h.signalExit(ctx, execution)
	default:
		return false, fmt.Errorf("agent: no short step %q", step.Kind)
	}
}

// deliverCredentials delivers or replaces the bound environment's
// credentials, as the authorities supply them now, and records what was
// delivered.
func (h *ReconcileExecutionHandler) deliverCredentials(ctx context.Context, execution *domain.AgentExecution) (bool, error) {
	supply, err := h.supplies.credentials(ctx, execution)
	if err != nil {
		return false, err
	}
	delivery, err := h.environments.DeliverCredentials(ctx, execution, supply, h.holding(execution))
	if err != nil {
		return false, err
	}
	delivery.ExpiresAt = supply.IdentityToken.redeliveryDue(delivery.ExpiresAt)
	return h.recorded(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordCredentialDelivery(delivery), nil
	})
}

// renewLease renews the bound environment's lease to the decided expiry and
// records the expiry the authority returned, which only ever extends it.
func (h *ReconcileExecutionHandler) renewLease(ctx context.Context, execution *domain.AgentExecution, step domain.ExecutionStep) (bool, error) {
	expiresAt, err := h.environments.RenewLease(ctx, execution, step.LeaseExpiresAt)
	if err != nil {
		return false, err
	}
	return h.recorded(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordLease(expiresAt), nil
	})
}

// syncInputs syncs a Managed Computer's input copies from the current files
// of its Work's located inputs and records the changes it covered. A failed
// attempt is counted on the execution (Input copy retry) and returned for its
// wake's retry, until the attempt that reaches the ceiling abandons the sync:
// that outcome is recorded and logged as an error, and nothing is left to
// retry. The recorded cause names the class of failure only; the error may
// carry what the Work's owners must not see, and goes to the log.
func (h *ReconcileExecutionHandler) syncInputs(ctx context.Context, execution *domain.AgentExecution) (bool, error) {
	inputs, err := h.supplies.inputs(ctx, execution.Launch.LocatedLayout.Inputs)
	if err == nil {
		var synced int64
		if synced, err = h.environments.SyncInputs(ctx, execution, inputs, h.holding(execution)); err == nil {
			return h.recorded(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
				return execution.RecordInputSync(synced, h.now()), nil
			})
		}
	}
	if ctx.Err() != nil || errors.Is(err, ErrEnvironmentActionRefused) {
		return false, err
	}
	cause := "syncing the input copies failed"
	if errors.Is(err, context.DeadlineExceeded) {
		cause = "syncing the input copies timed out"
	}
	abandoned := false
	recordErr := h.record(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		var changed bool
		changed, abandoned = execution.RecordInputSyncFailure(cause)
		return changed, nil
	})
	if recordErr != nil || !abandoned {
		return false, errors.Join(err, recordErr)
	}
	h.logger.ErrorContext(ctx, "input copy sync abandoned", slog.String("execution_id", execution.ID), slog.String("work_id", execution.WorkID),
		slog.Int("attempts", domain.InputSyncAttempts), slog.Any("error", err))
	return true, nil
}

func (h *ReconcileExecutionHandler) withdraw(ctx context.Context, execution *domain.AgentExecution) (bool, error) {
	if err := h.environments.WithdrawCredentials(ctx, execution); err != nil {
		return false, err
	}
	return h.recorded(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
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

func (h *ReconcileExecutionHandler) leaveDesk(ctx context.Context, execution *domain.AgentExecution) (bool, error) {
	if execution.Desk.ID != "" {
		if err := h.computers.LeaveDesk(ctx, execution.Desk.ID); err != nil {
			return false, err
		}
	}
	return h.recorded(ctx, execution.ID, func(execution *domain.AgentExecution) (bool, error) {
		return execution.RecordDeskLeft(h.now()), nil
	})
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

// cancelRunningTurn ends the Turn the Harness is running, if one is; asking
// again while its cancellation is pending, or after it ended, changes
// nothing.
func cancelRunningTurn(ctx context.Context, harness DispatcherPort, workID string) error {
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

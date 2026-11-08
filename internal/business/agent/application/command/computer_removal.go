package command

import (
	"context"
	"errors"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ComputerRemoved applies the removal of a Managed Computer to every Work
// whose run it may still hold, a start waiting in its line included: their
// execution environments are lost (Agent model: Computer removal). It
// applies what Computer says now, never the fact that woke it, so a fact
// about a Computer that is not removed changes nothing and a repeated one
// finds nothing left to change. A Work that cannot be changed now does not
// hold back the others; its recovery applies the removal later, and the
// failures are returned.
func (h *ComputerReachHandler) ComputerRemoved(ctx context.Context, computerID string) error {
	place, err := h.describe(ctx, computerID, true)
	if err != nil {
		return err
	}
	if !place.Managed || !place.Removed {
		return nil
	}
	var failures []error
	after := ""
	for {
		ids, err := h.works.ListHeldWorkIDs(ctx, computerID, after, computerWorksPage)
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		if len(ids) == 0 {
			return errors.Join(failures...)
		}
		for _, id := range ids {
			if err := h.loseEnvironment(ctx, id); err != nil {
				failures = append(failures, fmt.Errorf("work %s: %w", id, err))
			}
		}
		after = ids[len(ids)-1]
	}
}

// loseEnvironment lets the Work's current execution report its environment
// lost with its removed Computer, a start that waits in its line included,
// and applies the report to the Work.
func (h *ComputerReachHandler) loseEnvironment(ctx context.Context, workID string) error {
	if err := h.coordination.BeginExecution(ctx, workID); err != nil {
		return err
	}
	var err error
	for range reachAttempts {
		var execution *domain.AgentExecution
		if execution, err = h.executions.Latest(ctx, workID); errors.Is(err, domain.ErrExecutionNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		var decision domain.ExecutionDecision
		// A removed Computer took every environment with it; nothing is leased.
		if decision, err = execution.Reconcile(domain.EnvironmentObservation{Removed: true, LossReason: domain.ComputerRemovedReason}, domain.LeaseTerms{}, h.now()); err != nil {
			return err
		}
		if !decision.Changed {
			break
		}
		if err = h.executions.Save(ctx, execution); !errors.Is(err, domain.ErrConcurrentModification) {
			if err != nil {
				return err
			}
			break
		}
	}
	if err != nil {
		return err
	}
	return h.coordination.ApplyExecutionReports(ctx, workID)
}

// recoverRemoval applies, during a Work's recovery, the removal of its
// Managed Computer a lost Computer Removed did not bring. Only a Managed
// Work whose run may still be on its machine asks, sharing a recent read of
// its Computer with the other Works on it.
func (h *ComputerReachHandler) recoverRemoval(ctx context.Context, work *domain.AgentWork) (bool, error) {
	if h == nil || !work.OnManagedComputer() || !work.MayHoldEnvironment() {
		return false, nil
	}
	place, err := h.describe(ctx, work.ComputerID, false)
	if err != nil || !place.Managed || !place.Removed {
		return false, err
	}
	return true, h.loseEnvironment(ctx, work.ID)
}

package agent

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"xorm.io/xorm"
)

// committedExecutions decorates AgentExecution persistence at composition,
// after the commit: the execution's Domain Events are dispatched, its
// reconciliation is woken, and its Work's state, whose startup progress the
// execution's facts show, is published. Each is best effort: a lost event or
// wake is made up by the recovery sweep, which reconciles every unfinished
// execution and coordinates every unfinished Work from their retained facts.
type committedExecutions struct {
	*execution.AgentExecutionRepository
	updates *workStateUpdates
	events  domainevent.Dispatcher
	// wake is wired by RecoveryModule; without it a commit only dispatches
	// its events and publishes its Work's state.
	wake func(context.Context, string)
}

func newCommittedExecutions(db *xorm.Engine, now func() time.Time, updates *workStateUpdates, events domainevent.Dispatcher) *committedExecutions {
	return &committedExecutions{AgentExecutionRepository: execution.NewAgentExecutionRepository(db, now), updates: updates, events: events}
}

func (r *committedExecutions) Save(ctx context.Context, e *domain.AgentExecution) error {
	if err := r.AgentExecutionRepository.Save(ctx, e); err != nil {
		return err
	}
	r.updates.publish(ctx, e.WorkID)
	wake(ctx, r.wake, e.ID)
	if e.Events == nil || e.Events.Len() == 0 {
		return nil
	}
	if err := r.events.DispatchAll(ctx, e.Events.Drain()); err != nil {
		r.updates.logger.WarnContext(ctx, "dispatch execution events failed", slog.String("execution_id", e.ID), slog.String("agent_work_id", e.WorkID), slog.Any("error", err))
	}
	return nil
}

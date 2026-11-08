package infrastructure

import (
	"context"
	"time"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/xorm"
)

// Retain lifecycle evidence in the same transaction as the owning Session.
// Turn offsets, input receipts and other unrelated saves add no duplicate state;
// a Turn entering or leaving Waiting is a fact of its own.
func retainWorkState(tx *xorm.Session, session *domain.AgentWork, row *agentWorkDO) error {
	next := workStateHistoryDO{SessionID: row.ID, SourceVersion: row.Version, State: row.State,
		Failed: session.FailureReason != "" || session.State == domain.WorkFailed, Waiting: session.Waiting(), ObservedAt: row.UpdatedAt}
	if process := session.HarnessProcess; process != nil {
		next.Started = process.Ready
	}
	var previous workStateHistoryDO
	found, err := tx.Where("session_id=? AND deleted_at=0", row.ID).Desc("source_version").Get(&previous)
	if err != nil {
		return oops.Wrap(err)
	}
	if found && previous.State == next.State && previous.Started == next.Started && previous.Failed == next.Failed && previous.Waiting == next.Waiting {
		return nil
	}
	// A wall clock adjustment cannot move a later committed revision behind an
	// earlier fact. Revisions disambiguate transitions in the same millisecond.
	if found && next.ObservedAt < previous.ObservedAt {
		next.ObservedAt = previous.ObservedAt
	}
	next.ID = query.NewStateObservationID()
	next.Version = 1
	next.CreatedAt, next.UpdatedAt = row.UpdatedAt, row.UpdatedAt
	_, err = tx.Insert(&next)
	return oops.With("operation", "agent_session.retain_state").Wrap(err)
}

// ListWorkLifecycleEvents folds the retained state history into lifecycle
// transitions; waiting and startup flag changes within one state are omitted.
func (r *WorkQueryRepository) ListWorkLifecycleEvents(ctx context.Context, workID string) ([]query.WorkLifecycleEvent, error) {
	var rows []workStateHistoryDO
	if err := r.db.Context(ctx).Cols("state", "observed_at").Where("session_id = ? AND deleted_at = 0", workID).Asc("source_version").Find(&rows); err != nil {
		return nil, oops.With("operation", "agent.list_work_lifecycle").Wrap(err)
	}
	events := make([]query.WorkLifecycleEvent, 0, len(rows))
	for _, row := range rows {
		if int(row.State) >= len(sessionStates) {
			return nil, domain.ErrInvalidWork
		}
		state := sessionStates[row.State]
		if len(events) > 0 && events[len(events)-1].State == state {
			continue
		}
		events = append(events, query.WorkLifecycleEvent{State: state, OccurredAt: time.UnixMilli(row.ObservedAt).UTC()})
	}
	return events, nil
}

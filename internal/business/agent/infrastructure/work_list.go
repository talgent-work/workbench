package infrastructure

import (
	"context"
	"slices"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"xorm.io/builder"
)

// agentOwnerColumn reads a Work's Agent Owner, its definition's first Owner;
// Owners are kept sorted, so the first is stable. Work views and reports show
// it; no Work records an owner of its own.
const agentOwnerColumn = "(SELECT JSON_UNQUOTE(JSON_EXTRACT(d.owners, '$[0]')) FROM agent_definition d WHERE d.id = s.agent_id)"

// ListWorkViews lists every established Work matching the filter, whatever
// became of its first input: a caller associates a Work Agent created even when
// its first input was refused, so lists show what callers associate. A
// rejected creation establishes no Work to list.
func (r *WorkQueryRepository) ListWorkViews(ctx context.Context, filter query.WorkListFilter) ([]query.WorkSummary, error) {
	if filter.WorkCaller == nil && len(filter.WorkIDs) == 0 && filter.AgentOwner == "" {
		return nil, domain.ErrInvalidWork
	}
	q := r.db.Context(ctx).Table("agent_session").Alias("s").
		Select("s.id," + workFactSelect + "," + agentOwnerColumn + " AS agent_owner,s.started_by,s.state,s.harness_process,s.stop_reason,s.disconnected_since,s.stop_request,s.failure_reason,s.created_at,s.updated_at").
		Where("s.deleted_at=0")
	if caller := filter.WorkCaller; caller != nil {
		q = q.And("s.work_caller_kind=? AND s.work_caller_id=?", caller.Kind, caller.ID)
	}
	if key := filter.WorkKey; key.Present() {
		q = q.And("s.work_key_kind=? AND s.work_key_id=?", key.Kind, key.ID)
	}
	if filter.Purpose != "" {
		q = q.Join("INNER", []string{"agent_definition", "a"}, "a.id=s.agent_id AND a.deleted_at=0").And("a.purpose=?", filter.Purpose)
	}
	if len(filter.WorkIDs) > 0 {
		q = q.And(builder.In("s.id", filter.WorkIDs))
	}
	if filter.AgentOwner != "" {
		// An Agent Owner's Works are those of the definitions it owns.
		q = q.And("JSON_CONTAINS((SELECT o.owners FROM agent_definition o WHERE o.id = s.agent_id), JSON_QUOTE(?))", filter.AgentOwner)
	}
	if filter.ExtensionWorks {
		q = q.And(builder.NotIn("s.work_caller_kind", domain.CallerKindPerson, domain.CallerKindApplication))
	}
	if filter.ShareReader != "" {
		q = q.And("s.id IN (SELECT w.work_id FROM agent_work_share w WHERE w.reader_id=?)", filter.ShareReader)
	}
	if filter.Archived != nil {
		closed := slices.Index(sessionStates[:], domain.WorkClosed)
		if *filter.Archived {
			q = q.And("s.state=?", closed)
		} else {
			q = q.And("s.state<>?", closed)
		}
	}
	if filter.AfterID != "" {
		q = q.And("s.id>?", filter.AfterID)
	}
	if filter.Recent {
		q = q.Desc("s.updated_at", "s.created_at", "s.id")
	} else {
		q = q.Asc("s.id")
	}
	var rows []workViewRow
	if err := q.Limit(filter.Limit).Find(&rows); err != nil {
		return nil, oops.With("operation", "agent.list_work_views").Wrap(err)
	}
	result := make([]query.WorkSummary, 0, len(rows))
	for _, row := range rows {
		work, err := workViewFromRow(row)
		if err != nil {
			return nil, err
		}
		result = append(result, query.WorkSummary{WorkID: work.WorkID, WorkCaller: work.WorkCaller, WorkKey: work.WorkKey, FileHome: work.FileHome, AgentOwner: work.AgentOwner, StartedBy: work.StartedBy, State: work.State, StateReason: work.StateReason, ArchivePending: work.ArchivePending, Waiting: work.Waiting, DisconnectedSince: work.DisconnectedSince, CreatedAt: work.CreatedAt, UpdatedAt: work.UpdatedAt})
	}
	return result, nil
}

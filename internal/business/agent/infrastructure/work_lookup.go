package infrastructure

import (
	"context"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// FindBySubject returns the definition's Work for the Subject, a closed Work
// included; storage keeps one row per definition and Subject.
func (r *AgentWorkRepository) FindBySubject(ctx context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	var row agentWorkDO
	found, err := r.db.Context(ctx).Cols("id").Where("agent_id = ? AND subject_kind = ? AND subject_id = ? AND deleted_at = 0", agentID, string(subject.Kind), subject.ID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "agent_work.find_subject", "agent_id", agentID).Wrap(err)
	}
	if !found {
		return nil, nil
	}
	return r.Get(ctx, row.ID)
}

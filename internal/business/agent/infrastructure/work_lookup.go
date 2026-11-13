package infrastructure

import (
	"context"

	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// FindByWorkKey returns the definition's Work for the Work Key, a closed Work
// included; storage keeps one row per definition and Work Key.
func (r *AgentWorkRepository) FindByWorkKey(ctx context.Context, agentID string, key domain.WorkKey) (*domain.AgentWork, error) {
	var row agentWorkDO
	found, err := r.db.Context(ctx).Cols("id").Where("agent_id = ? AND work_key_kind = ? AND work_key_id = ? AND deleted_at = 0", agentID, key.Kind, key.ID).Get(&row)
	if err != nil {
		return nil, oops.With("operation", "agent_work.find_work_key", "agent_id", agentID).Wrap(err)
	}
	if !found {
		return nil, nil
	}
	return r.Get(ctx, row.ID)
}

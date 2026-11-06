package infrastructure

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var _ command.InputAcceptances = (*AgentWorkRepository)(nil)

// CommitAcceptance saves the Work changed by accepting input, against the
// Work version the acceptance read.
func (r *AgentWorkRepository) CommitAcceptance(ctx context.Context, work *domain.AgentWork) error {
	if work == nil || work.Version == 0 {
		return domain.ErrInvalidWork
	}
	return r.saveWith(ctx, work, "", 0, nil, saveHooks{})
}

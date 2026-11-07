package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

// WorkStartup reports a Work's start request identities, derived from its
// facts, not Session lifecycle: its creation request, its latest Resume or
// Restart request, whether it still holds its initial task input not yet
// handed off to its input buffer (CreationPending), and whether it closed for
// good, which ends its first start's preparation (CleanupConfirmed).
type WorkStartup struct {
	WorkID, InitialRequestID, ContinuationRequestID string
	CreationPending, CleanupConfirmed               bool
}

// ExecutionQueryRepository reads startup obligations, recovery candidates
// (the Works and the AgentExecutions with unfinished obligations) and the
// current run's Plugin outcomes.
type ExecutionQueryRepository interface {
	WorkStartupReader
	RecoveryWorkQueryRepository
	ExecutionRecoveryReader
	PluginResultReader
}

type WorkStartupReader interface {
	GetWorkStartup(context.Context, string) (WorkStartup, error)
}

type GetWorkStartupHandler struct {
	reader WorkStartupReader
	access access.WorkAuthorizer
}

func NewGetWorkStartupHandler(reader WorkStartupReader, authorization access.WorkAuthorizer) *GetWorkStartupHandler {
	return &GetWorkStartupHandler{reader: reader, access: authorization}
}

func (h *GetWorkStartupHandler) Handle(ctx context.Context, input GetWork) (WorkStartup, error) {
	if err := h.access.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return WorkStartup{}, err
	}
	return h.reader.GetWorkStartup(ctx, input.WorkID)
}

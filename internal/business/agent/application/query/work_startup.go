package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

// WorkStartup reports first-start request facts, not Session lifecycle.
// CreationPending reports that the Work still holds its initial task input,
// not yet handed off to its input buffer.
type WorkStartup struct {
	ContinuationRequestID      string
	ContinuationPending        bool
	WorkID, InitialRequestID   string
	Rejected, CleanupConfirmed bool
	CreationPending            bool
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
	reader        WorkStartupReader
	continuations ContinuationReader
	access        access.WorkAuthorizer
}

func NewGetWorkStartupHandler(reader WorkStartupReader, authorization access.WorkAuthorizer, continuations ContinuationReader) *GetWorkStartupHandler {
	return &GetWorkStartupHandler{reader: reader, access: authorization, continuations: continuations}
}

func (h *GetWorkStartupHandler) Handle(ctx context.Context, input GetWork) (WorkStartup, error) {
	if err := h.access.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return WorkStartup{}, err
	}
	result, err := h.reader.GetWorkStartup(ctx, input.WorkID)
	if err != nil {
		return WorkStartup{}, err
	}
	continuation, err := h.continuations.GetWorkContinuation(ctx, input.WorkID)
	if err != nil {
		return WorkStartup{}, err
	}
	result.ContinuationRequestID, result.ContinuationPending = continuation.RequestID, continuation.Pending
	return result, nil
}

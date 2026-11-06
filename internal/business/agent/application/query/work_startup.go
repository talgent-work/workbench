package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

// WorkStartup reports application-owned request facts, not Session lifecycle.
// CreationPending includes a later explicit request before its initial-input
// handoff; the preceding rejected initial request alone cannot prove quiescence.
type WorkStartup struct {
	ContinuationRequestID      string
	ContinuationPending        bool
	WorkID, InitialRequestID   string
	Rejected, CleanupConfirmed bool
	CreationPending            bool
}

// ExecutionQueryRepository reads startup obligations and recovery candidates.
type ExecutionQueryRepository interface {
	WorkStartupReader
	RecoveryWorkQueryRepository
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

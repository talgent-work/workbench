package query

import "context"

// InitialExecutionRecoveryReader scans durable preparation and cleanup obligations.
// Completed inputs and confirmed rejection cleanup are excluded by persistence.
type InitialExecutionRecoveryReader interface {
	ListPendingInitialExecutions(context.Context, string, int) ([]string, error)
}

type ListInitialExecutionRecoveryHandler struct {
	requests InitialExecutionRecoveryReader
}

func NewListInitialExecutionRecoveryHandler(requests InitialExecutionRecoveryReader) *ListInitialExecutionRecoveryHandler {
	return &ListInitialExecutionRecoveryHandler{requests: requests}
}
func (h *ListInitialExecutionRecoveryHandler) Handle(ctx context.Context, after string) ([]string, error) {
	return h.requests.ListPendingInitialExecutions(ctx, after, 100)
}

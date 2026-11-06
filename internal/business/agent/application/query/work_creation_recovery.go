package query

import "context"

type WorkCreationRecoveryReader interface {
	ListPendingWorkCreations(context.Context, string, int) ([]string, error)
}

type ListWorkCreationRecoveryHandler struct{ requests WorkCreationRecoveryReader }

func NewListWorkCreationRecoveryHandler(requests WorkCreationRecoveryReader) *ListWorkCreationRecoveryHandler {
	return &ListWorkCreationRecoveryHandler{requests: requests}
}
func (h *ListWorkCreationRecoveryHandler) Handle(ctx context.Context, after string) ([]string, error) {
	return h.requests.ListPendingWorkCreations(ctx, after, 100)
}

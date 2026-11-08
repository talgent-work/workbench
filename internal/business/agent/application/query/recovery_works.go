package query

import "context"

type ListRecoveryWorksHandler struct {
	repository ExecutionQueryRepository
}

func NewListRecoveryWorksHandler(repository ExecutionQueryRepository) *ListRecoveryWorksHandler {
	return &ListRecoveryWorksHandler{repository: repository}
}

func (h *ListRecoveryWorksHandler) Handle(ctx context.Context, afterID string) ([]string, error) {
	return h.repository.ListWorkIDs(ctx, afterID, 100)
}

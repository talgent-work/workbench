package query

import "context"

// RecoveryWorkQueryRepository lists conversations holding execution grants,
// including completed Sessions whose grant release was interrupted.
type RecoveryWorkQueryRepository interface {
	ListWorkIDs(context.Context, string, int) ([]string, error)
}

type ListRecoveryWorksHandler struct {
	repository RecoveryWorkQueryRepository
}

func NewListRecoveryWorksHandler(repository RecoveryWorkQueryRepository) *ListRecoveryWorksHandler {
	return &ListRecoveryWorksHandler{repository: repository}
}

func (h *ListRecoveryWorksHandler) Handle(ctx context.Context, afterID string) ([]string, error) {
	return h.repository.ListWorkIDs(ctx, afterID, 100)
}

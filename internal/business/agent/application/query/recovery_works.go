package query

import "context"

// ExecutionQueryRepository reads recovery candidates (the Works and the
// AgentExecutions with unfinished obligations) and the current run's Plugin
// outcomes.
type ExecutionQueryRepository interface {
	RecoveryWorkQueryRepository
	ExecutionRecoveryReader
	PluginResultReader
}

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

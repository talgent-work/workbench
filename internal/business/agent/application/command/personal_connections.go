package command

import (
	"context"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (h *CreateWorkHandler) repositoryInput(ctx context.Context, owner string, initial *PrepareWorkExecution) (domain.WorkRepositoryInput, error) {
	if initial == nil || len(initial.Selections) == 0 {
		return domain.WorkRepositoryInput{}, nil
	}
	if owner == "" {
		return domain.WorkRepositoryInput{}, fmt.Errorf("%w: repositories require a represented human or an owning Application", domain.ErrInvalidWork)
	}
	facts, err := h.catalog.ListAccessibleRepositories(ctx, owner)
	if err != nil {
		return domain.WorkRepositoryInput{}, err
	}
	input := domain.WorkRepositoryInput{OwnerUserID: owner, WorkBranch: initial.WorkBranch, Available: facts}
	for _, choice := range initial.Selections {
		input.Choices = append(input.Choices, domain.WorkRepositoryChoice{RepositoryID: choice.RepositoryID, BaseBranch: choice.BaseBranch})
	}
	return input, nil
}

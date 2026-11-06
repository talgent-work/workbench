package command

import (
	"context"
	"fmt"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// PersonalConnectionsPort reads personal connection facts under Marketplace's
// trusted owner scope. It never supplies credentials or grants continuing access.
type PersonalConnectionsPort interface {
	ListPersonalAuthorizations(context.Context, string) ([]domain.PersonalAuthorization, error)
	ListAccessibleRepositories(context.Context, string) ([]domain.AccessibleRepository, error)
}

func (h *CreateWorkHandler) repositoryInput(ctx context.Context, owner string, initial *PrepareWorkExecution) (domain.WorkRepositoryInput, error) {
	if initial == nil || len(initial.Selections) == 0 {
		return domain.WorkRepositoryInput{}, nil
	}
	if owner == "" {
		return domain.WorkRepositoryInput{}, fmt.Errorf("%w: personal repositories require a represented human", domain.ErrInvalidWork)
	}
	facts, err := h.connections.ListAccessibleRepositories(ctx, owner)
	if err != nil {
		return domain.WorkRepositoryInput{}, err
	}
	input := domain.WorkRepositoryInput{OwnerUserID: owner, Available: facts}
	for _, choice := range initial.Selections {
		input.Choices = append(input.Choices, domain.WorkRepositoryChoice{RepositoryID: choice.RepositoryID, BaseBranch: choice.BaseBranch})
	}
	return input, nil
}

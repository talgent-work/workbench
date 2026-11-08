package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (a *Application) GetWorkServiceAccess(ctx context.Context, input ControlWork) (query.WorkServiceAccess, error) {
	if strings.TrimSpace(input.WorkID) == "" {
		return query.WorkServiceAccess{}, domain.ErrInvalidWork
	}
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return query.WorkServiceAccess{}, err
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return query.WorkServiceAccess{}, err
	}
	observations, err := a.ExecutionQueries.ReadServiceAccess(ctx, work.ID)
	if err != nil {
		return query.WorkServiceAccess{}, err
	}
	repositories, err := a.ExecutionQueries.ReadRepositoryPreparation(ctx, work.ID)
	if err != nil {
		return query.WorkServiceAccess{}, err
	}
	return AssembleWorkServiceAccess(work, observations, repositories), nil
}

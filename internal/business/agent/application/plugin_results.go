package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// GetWorkPluginResults lists each Plugin the Work selected with its outcome
// in the Work's current run, for a reader the Work admits.
func (a *Application) GetWorkPluginResults(ctx context.Context, input ControlWork) ([]query.PluginResult, error) {
	if strings.TrimSpace(input.WorkID) == "" {
		return nil, domain.ErrInvalidWork
	}
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return nil, err
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return nil, err
	}
	outcomes, err := a.PluginResults.ListCurrentPluginResults(ctx, work.ID)
	if err != nil {
		return nil, err
	}
	return AssembleWorkPluginResults(work, outcomes), nil
}

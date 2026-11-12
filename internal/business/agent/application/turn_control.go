package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type CancelTurn struct{ ActorID, WorkID, TurnID string }

func (a *Application) CancelTurn(ctx context.Context, input CancelTurn) error {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorID, input.WorkID); err != nil {
		return err
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if err != nil {
		return err
	}
	if err := work.RequireReadyProcess(); err != nil {
		return err
	}
	if strings.TrimSpace(input.TurnID) == "" {
		return domain.ErrInvalidWork
	}
	// No interrupt reaches an environment the platform does not observe.
	if work.Disconnected() {
		return domain.ErrExecutionUnobserved
	}
	return a.Harness.CancelTurn(ctx, work.ID, input.TurnID)
}
func (a *Application) GetCurrentTurn(ctx context.Context, input ControlWork) (command.CurrentTurn, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorID, input.WorkID); err != nil {
		return command.CurrentTurn{}, err
	}
	work, err := a.WorkQueries.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return command.CurrentTurn{}, err
	}
	if work.State != "working" {
		return command.CurrentTurn{}, nil
	}
	// During a Disconnection the live Turn is not observed; the Work shows
	// the Disconnection instead.
	if !work.DisconnectedSince.IsZero() {
		return command.CurrentTurn{}, domain.ErrExecutionUnobserved
	}
	return a.Harness.GetCurrentTurn(ctx, input.WorkID)
}

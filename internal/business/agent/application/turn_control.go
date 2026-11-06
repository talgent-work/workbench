package application

import (
	"context"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type CancelTurn struct{ ActorUserID, WorkID, TurnID string }
type CurrentTurn struct {
	ID              string
	CancelRequested bool
	Active          bool
}

// Harness turn control does not change AgentWork lifecycle or stop its process.
type TurnControlPort interface {
	CancelTurn(ctx context.Context, workID, turnID string) error
	GetCurrentTurn(ctx context.Context, workID string) (CurrentTurn, error)
}

func (a *Application) CancelTurn(ctx context.Context, input CancelTurn) error {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
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
	return a.Turns.CancelTurn(ctx, work.ID, input.TurnID)
}
func (a *Application) GetCurrentTurn(ctx context.Context, input ControlWork) (CurrentTurn, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return CurrentTurn{}, err
	}
	work, err := a.WorkViews.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return CurrentTurn{}, err
	}
	if work.State != "working" {
		return CurrentTurn{}, nil
	}
	// During a Disconnection the live Turn is not observed; the Work shows
	// the Disconnection instead.
	if !work.DisconnectedSince.IsZero() {
		return CurrentTurn{}, domain.ErrExecutionUnobserved
	}
	return a.Turns.GetCurrentTurn(ctx, input.WorkID)
}

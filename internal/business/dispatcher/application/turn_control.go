package application

import "context"

type CancelTurn struct {
	WorkID string
	TurnID string
}
type CurrentTurn struct {
	ID              string
	CancelRequested bool
	Active          bool
}
type TurnControlPort interface {
	CancelTurn(context.Context, CancelTurn) error
	GetCurrentTurn(ctx context.Context, workID string) (CurrentTurn, error)
}

func (a *Application) CancelTurn(ctx context.Context, input CancelTurn) error {
	return a.turns.CancelTurn(ctx, input)
}
func (a *Application) GetCurrentTurn(ctx context.Context, workID string) (CurrentTurn, error) {
	return a.turns.GetCurrentTurn(ctx, workID)
}

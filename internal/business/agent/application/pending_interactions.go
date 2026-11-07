package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (a *Application) ListPendingInteractions(ctx context.Context, input ControlWork) ([]command.PendingInteraction, error) {
	if err := a.WorkPrincipals.AuthorizeWork(ctx, input.ActorUserID, input.WorkID); err != nil {
		return nil, err
	}
	work, err := a.WorkViews.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return nil, err
	}
	if work.State != "working" {
		return nil, nil
	}
	// The Harness's live requests are not observed during a Disconnection.
	if !work.DisconnectedSince.IsZero() {
		return nil, domain.ErrExecutionUnobserved
	}
	// Dispatcher answers ReplySubmitted from the Work's input buffer.
	return a.Harness.ListPendingInteractions(ctx, input.WorkID)
}

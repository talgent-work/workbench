package application

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type PendingInteraction struct {
	ID, ToolCallID, ToolName, Prompt string
	CanAlwaysAllow, ReplySubmitted   bool
	QuestionInputJSON, TurnID        string
	Agent                            *string
}

type PendingInteractionsPort interface {
	ListPendingInteractions(ctx context.Context, workID string) ([]PendingInteraction, error)
}

func (a *Application) ListPendingInteractions(ctx context.Context, input ControlWork) ([]PendingInteraction, error) {
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
	return a.Interactions.ListPendingInteractions(ctx, input.WorkID)
}

package application

import "context"

type PendingInteraction struct {
	ID, ToolCallID, ToolName, Prompt string
	CanAlwaysAllow, ReplySubmitted   bool
	QuestionInputJSON, TurnID        string
	Agent                            *string
}

type PendingInteractionsPort interface {
	ListPendingInteractions(ctx context.Context, workID string) ([]PendingInteraction, error)
}

func (a *Application) ListPendingInteractions(ctx context.Context, workID string) ([]PendingInteraction, error) {
	return a.interactions.ListPendingInteractions(ctx, workID)
}

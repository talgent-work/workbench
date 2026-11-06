package execution

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (c *DispatcherControl) ListPendingInteractions(ctx context.Context, workID string) ([]application.PendingInteraction, error) {
	if strings.TrimSpace(workID) == "" {
		return nil, domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return nil, domain.ErrExecutionRequest
	}
	request := connect.NewRequest(&dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: workID})
	request.Header().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.ListPendingInteractions(ctx, request)
	if connect.CodeOf(err) == connect.CodeUnavailable {
		return nil, unobservedError(ctx, workID, "pending interactions", err)
	}
	if err != nil {
		return nil, controlError(ctx, workID, "pending interactions", err)
	}
	result := make([]application.PendingInteraction, 0, len(response.Msg.Interactions))
	for _, item := range response.Msg.Interactions {
		result = append(result, application.PendingInteraction{ID: item.GetId(), ToolCallID: item.GetToolCallId(), ToolName: item.GetToolName(), Prompt: item.GetPrompt(), CanAlwaysAllow: item.GetCanAlwaysAllow(), QuestionInputJSON: item.GetQuestionInputJson(), ReplySubmitted: item.GetReplySubmitted(), TurnID: item.GetTurnId(), Agent: item.Agent})
	}
	return result, nil
}

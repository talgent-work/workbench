package execution

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var _ application.TurnControlPort = (*DispatcherControl)(nil)

func (c *DispatcherControl) CancelTurn(ctx context.Context, workID string, turnID string) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return domain.ErrExecutionRequest
	}
	request := connect.NewRequest(&dispatcherprivatev1.CancelTurnRequest{WorkId: workID, TurnId: turnID})
	request.Header().Set("Authorization", "Bearer "+c.token)
	_, err := c.client.CancelTurn(ctx, request)
	switch connect.CodeOf(err) {
	case connect.CodeFailedPrecondition:
		return errors.Join(domain.ErrWorkTransition, controlError(ctx, workID, "cancel turn", err))
	case connect.CodeNotFound, connect.CodeUnavailable:
		// No replica reads the Work's stream: it is reattaching or lost.
		return unobservedError(ctx, workID, "cancel turn", err)
	}
	return controlError(ctx, workID, "cancel turn", err)
}
func (c *DispatcherControl) GetCurrentTurn(ctx context.Context, workID string) (application.CurrentTurn, error) {
	if strings.TrimSpace(workID) == "" {
		return application.CurrentTurn{}, domain.ErrExecutionRequest
	}
	if strings.TrimSpace(c.token) == "" {
		return application.CurrentTurn{}, domain.ErrExecutionRequest
	}
	request := connect.NewRequest(&dispatcherprivatev1.GetCurrentTurnRequest{WorkId: workID})
	request.Header().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.GetCurrentTurn(ctx, request)
	if connect.CodeOf(err) == connect.CodeUnavailable {
		return application.CurrentTurn{}, unobservedError(ctx, workID, "current turn", err)
	}
	if err != nil {
		return application.CurrentTurn{}, controlError(ctx, workID, "current turn", err)
	}
	return application.CurrentTurn{ID: response.Msg.TurnId, CancelRequested: response.Msg.CancelRequested, Active: response.Msg.Active}, nil
}

package execution

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func (c *DispatcherControl) GetWorkCapabilities(ctx context.Context, workID string) (command.WorkCapabilities, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(c.token) == "" {
		return command.WorkCapabilities{}, domain.ErrExecutionRequest
	}
	request := &dispatcherprivatev1.GetCapabilitiesRequest{WorkId: workID}
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.GetCapabilities(ctx, request)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeFailedPrecondition || connect.CodeOf(err) == connect.CodeNotFound {
			return command.WorkCapabilities{}, errors.Join(application.ErrWorkCapabilitiesUnavailable, controlError(ctx, workID, "capabilities", err))
		}
		return command.WorkCapabilities{}, controlError(ctx, workID, "capabilities", err)
	}
	result := command.WorkCapabilities{Observed: response.Observed}
	for _, item := range response.Commands {
		result.Commands = append(result.Commands, command.WorkCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	return result, nil
}

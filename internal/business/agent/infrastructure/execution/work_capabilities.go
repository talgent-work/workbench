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

var _ application.WorkCapabilitiesPort = (*DispatcherControl)(nil)

func (c *DispatcherControl) GetWorkCapabilities(ctx context.Context, workID string) (application.WorkCapabilities, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(c.token) == "" {
		return application.WorkCapabilities{}, domain.ErrExecutionRequest
	}
	request := connect.NewRequest(&dispatcherprivatev1.GetCapabilitiesRequest{WorkId: workID})
	request.Header().Set("Authorization", "Bearer "+c.token)
	response, err := c.client.GetCapabilities(ctx, request)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeFailedPrecondition || connect.CodeOf(err) == connect.CodeNotFound {
			return application.WorkCapabilities{}, errors.Join(application.ErrWorkCapabilitiesUnavailable, controlError(ctx, workID, "capabilities", err))
		}
		return application.WorkCapabilities{}, controlError(ctx, workID, "capabilities", err)
	}
	result := application.WorkCapabilities{Observed: response.Msg.Observed}
	for _, item := range response.Msg.Commands {
		result.Commands = append(result.Commands, application.WorkCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	return result, nil
}

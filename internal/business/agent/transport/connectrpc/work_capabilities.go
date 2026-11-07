package connectrpc

import (
	"context"
	"strings"

	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *WorkHandler) GetWorkCapabilities(ctx context.Context, request *agentpublicv1.GetWorkCapabilitiesRequest) (*agentpublicv1.GetWorkCapabilitiesResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	value, err := h.application.GetWorkCapabilities(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.GetWorkId())})
	if err != nil {
		return nil, mapAgentError(err)
	}
	result := &agentpublicv1.GetWorkCapabilitiesResponse{Observed: value.Observed}
	for _, item := range value.Commands {
		result.Commands = append(result.Commands, &agentpublicv1.WorkCommand{Name: item.Name, Description: item.Description, ArgumentHint: item.ArgumentHint})
	}
	return result, nil
}

package connectrpc

import (
	"context"
	"strings"

	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

func (h *WorkHandler) GetWorkServiceAccess(ctx context.Context, request *agentpublicv1.GetWorkServiceAccessRequest) (*agentpublicv1.GetWorkServiceAccessResponse, error) {
	actor, _ := authn.CurrentUserID(ctx)
	result, err := h.application.GetWorkServiceAccess(ctx, application.ControlWork{ActorUserID: actor, WorkID: strings.TrimSpace(request.WorkId)})
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentpublicv1.GetWorkServiceAccessResponse{OwnerUserId: result.OwnerUserID, SelectedServices: result.SelectedServices}
	for _, observation := range result.Observations {
		response.Observations = append(response.Observations, &agentpublicv1.WorkServiceAccessObservation{Service: observation.Service, Target: observation.Target, Plugin: observation.Plugin, Mcp: observation.MCP, Status: observation.Status, Reason: observation.Reason})
	}
	for _, repository := range result.Repositories {
		response.Repositories = append(response.Repositories, &agentpublicv1.WorkRepositoryPreparation{RepositoryId: repository.RepositoryID, TargetPath: repository.TargetPath, Prepared: repository.Prepared, Reason: repository.Reason})
	}
	return response, nil
}

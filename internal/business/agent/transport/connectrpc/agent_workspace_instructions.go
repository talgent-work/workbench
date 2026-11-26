package connectrpc

import (
	"context"
	"strings"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// GetAgentWorkspaceInstructions reads an Agent's Workspace Instructions for
// one of its Owners, a person or the Application, or for an Application's
// Agent's Application Owner.
func (h *WorkHandler) GetAgentWorkspaceInstructions(ctx context.Context, request *agentpublicv1.GetAgentWorkspaceInstructionsRequest) (*agentpublicv1.GetAgentWorkspaceInstructionsResponse, error) {
	agentID := strings.TrimSpace(request.GetAgentId())
	requester, err := h.definitionRequester(ctx, agentID, domain.ErrWorkspaceInstructionsForbidden)
	if err != nil {
		return nil, err
	}
	instructions, err := h.application.Queries.GetAgentWorkspaceInstructions.Handle(ctx, agentID, requester)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.GetAgentWorkspaceInstructionsResponse{Instructions: instructions}, nil
}

// ChangeAgentWorkspaceInstructions replaces an Agent's Workspace Instructions
// for one of its Owners, a person or the Application, or for an Application's
// Agent's Application Owner.
func (h *WorkHandler) ChangeAgentWorkspaceInstructions(ctx context.Context, request *agentpublicv1.ChangeAgentWorkspaceInstructionsRequest) (*agentpublicv1.ChangeAgentWorkspaceInstructionsResponse, error) {
	agentID := strings.TrimSpace(request.GetAgentId())
	requester, err := h.definitionRequester(ctx, agentID, domain.ErrWorkspaceInstructionsForbidden)
	if err != nil {
		return nil, err
	}
	instructions, err := h.application.Commands.ChangeAgentWorkspaceInstructions.Handle(ctx, command.ChangeAgentWorkspaceInstructions{AgentID: agentID, Requester: requester, Instructions: request.GetInstructions()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.ChangeAgentWorkspaceInstructionsResponse{Instructions: instructions}, nil
}

package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// GetAgentWorkspaceInstructionsHandler reads a definition's Workspace
// Instructions for one of its Owners or an Application's Agent's Application
// Owner.
type GetAgentWorkspaceInstructionsHandler struct {
	agents domain.AgentDefinitionRepository
}

func NewGetAgentWorkspaceInstructionsHandler(agents domain.AgentDefinitionRepository) *GetAgentWorkspaceInstructionsHandler {
	return &GetAgentWorkspaceInstructionsHandler{agents: agents}
}

func (h *GetAgentWorkspaceInstructionsHandler) Handle(ctx context.Context, agentID string, requester domain.DefinitionRequester) (string, error) {
	agent, err := h.agents.Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	return agent.ReadWorkspaceInstructions(requester)
}

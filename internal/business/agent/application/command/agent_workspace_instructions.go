package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ChangeAgentWorkspaceInstructions carries the trusted requester and the new
// Workspace Instructions.
type ChangeAgentWorkspaceInstructions struct {
	AgentID, Instructions string
	Requester             domain.DefinitionRequester
}

type ChangeAgentWorkspaceInstructionsHandler struct {
	agents domain.AgentDefinitionRepository
}

func NewChangeAgentWorkspaceInstructionsHandler(agents domain.AgentDefinitionRepository) *ChangeAgentWorkspaceInstructionsHandler {
	return &ChangeAgentWorkspaceInstructionsHandler{agents: agents}
}

// Handle replaces the Workspace Instructions for one of the definition's
// Owners, or an Application's Agent's Application Owner, and returns them as
// saved, retrying a concurrent change of the definition.
func (h *ChangeAgentWorkspaceInstructionsHandler) Handle(ctx context.Context, input ChangeAgentWorkspaceInstructions) (string, error) {
	for range 8 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		agent, err := h.agents.Get(ctx, input.AgentID)
		if err != nil {
			return "", err
		}
		changed, err := agent.ChangeWorkspaceInstructions(input.Requester, input.Instructions)
		if err != nil || !changed {
			return agent.WorkspaceInstructions, err
		}
		if err = h.agents.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return agent.WorkspaceInstructions, err
	}
	return "", domain.ErrConcurrentModification
}

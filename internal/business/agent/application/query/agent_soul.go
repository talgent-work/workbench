package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// GetAgentSoulHandler reads a definition's Soul for one of its Owners or an
// Application's Agent's Application Owner.
type GetAgentSoulHandler struct {
	agents domain.AgentDefinitionRepository
}

func NewGetAgentSoulHandler(agents domain.AgentDefinitionRepository) *GetAgentSoulHandler {
	return &GetAgentSoulHandler{agents: agents}
}

func (h *GetAgentSoulHandler) Handle(ctx context.Context, agentID string, requester domain.DefinitionRequester) (string, error) {
	agent, err := h.agents.Get(ctx, agentID)
	if err != nil {
		return "", err
	}
	return agent.ReadSoul(requester)
}

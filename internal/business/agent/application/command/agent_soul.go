package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ChangeAgentSoul carries the trusted requester and the new Soul.
type ChangeAgentSoul struct {
	AgentID, Requester, Soul string
}

type ChangeAgentSoulHandler struct {
	agents domain.AgentDefinitionRepository
}

func NewChangeAgentSoulHandler(agents domain.AgentDefinitionRepository) *ChangeAgentSoulHandler {
	return &ChangeAgentSoulHandler{agents: agents}
}

// Handle replaces the Soul for one of the definition's Owners and returns it
// as saved, retrying a concurrent change of the definition.
func (h *ChangeAgentSoulHandler) Handle(ctx context.Context, input ChangeAgentSoul) (string, error) {
	for range 8 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		agent, err := h.agents.Get(ctx, input.AgentID)
		if err != nil {
			return "", err
		}
		changed, err := agent.ChangeSoul(input.Requester, input.Soul)
		if err != nil || !changed {
			return agent.Soul, err
		}
		if err = h.agents.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return agent.Soul, err
	}
	return "", domain.ErrConcurrentModification
}

package command

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ModelOffers supplies what a default model offers: whether the platform
// provides it now and its Model Template's thinking levels; a zero selection
// asks about the first provided Model Configuration.
type ModelOffers interface {
	Offer(selection domain.WorkModelSelection) domain.ModelOffer
}

// ChangeAgentWorkDefaults carries the trusted requester and the new Work
// defaults of an Application's Agent.
type ChangeAgentWorkDefaults struct {
	AgentID   string
	Requester domain.DefinitionRequester
	Defaults  domain.WorkDefaults
}

type ChangeAgentWorkDefaultsHandler struct {
	agents domain.AgentDefinitionRepository
	models ModelOffers
}

func NewChangeAgentWorkDefaultsHandler(agents domain.AgentDefinitionRepository, models ModelOffers) *ChangeAgentWorkDefaultsHandler {
	return &ChangeAgentWorkDefaultsHandler{agents: agents, models: models}
}

// Handle replaces the Work defaults for the Application or its Application
// Owner and returns them as saved, retrying a concurrent change of the
// definition. Works created earlier keep what they started with.
func (h *ChangeAgentWorkDefaultsHandler) Handle(ctx context.Context, input ChangeAgentWorkDefaults) (domain.WorkDefaults, error) {
	offer := h.models.Offer(domain.WorkModelSelection{ConfigurationID: strings.TrimSpace(input.Defaults.ModelConfigurationID)})
	for range 8 {
		if err := ctx.Err(); err != nil {
			return domain.WorkDefaults{}, err
		}
		agent, err := h.agents.Get(ctx, input.AgentID)
		if err != nil {
			return domain.WorkDefaults{}, err
		}
		changed, err := agent.ChangeWorkDefaults(input.Requester, input.Defaults, offer)
		if err != nil || !changed {
			return agent.WorkDefaults, err
		}
		if err = h.agents.Save(ctx, agent); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		return agent.WorkDefaults, err
	}
	return domain.WorkDefaults{}, domain.ErrConcurrentModification
}

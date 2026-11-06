package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type AgentDefinition struct {
	Owners       []string
	AgentName    string
	Purpose      string
	UsageScope   domain.UsageScope
	HomeBinding  domain.HomeBinding
	Visibility   domain.VisibilityRule
	ID           string
	Capabilities domain.CapabilityConfiguration
}

type GetAgentDefinitionHandler struct {
	repository domain.AgentDefinitionRepository
	assemble   func(*domain.AgentDefinition) AgentDefinition
}

func NewGetAgentDefinitionHandler(repository domain.AgentDefinitionRepository, assemble func(*domain.AgentDefinition) AgentDefinition) *GetAgentDefinitionHandler {
	return &GetAgentDefinitionHandler{repository: repository, assemble: assemble}
}

func (h *GetAgentDefinitionHandler) Handle(ctx context.Context, id string) (AgentDefinition, error) {
	agent, err := h.repository.Get(ctx, id)
	if err != nil {
		return AgentDefinition{}, err
	}
	return h.assemble(agent), nil
}

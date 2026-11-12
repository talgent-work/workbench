package connectrpc

import (
	"context"
	"strings"

	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// EnsureApplicationAgent returns the Agent of an Application the signed-in
// person owns, establishing it on its first need.
func (h *WorkHandler) EnsureApplicationAgent(ctx context.Context, request *agentpublicv1.EnsureApplicationAgentRequest) (*agentpublicv1.EnsureApplicationAgentResponse, error) {
	person, ok := authn.CurrentUserID(ctx)
	if !ok || person == "" {
		return nil, errNoPerson
	}
	agent, err := h.application.EnsureOwnedApplicationAgent(ctx, person, request.GetApplicationId())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.EnsureApplicationAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
}

// GetAgentWorkDefaults reads an Application's Agent's Work defaults for the
// Application or its Application Owner.
func (h *WorkHandler) GetAgentWorkDefaults(ctx context.Context, request *agentpublicv1.GetAgentWorkDefaultsRequest) (*agentpublicv1.GetAgentWorkDefaultsResponse, error) {
	configurer, ok := currentConfigurer(ctx)
	if !ok {
		return nil, errNoOwner
	}
	value, err := h.application.GetAgentWorkDefaults(ctx, configurer, strings.TrimSpace(request.GetAgentId()))
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.GetAgentWorkDefaultsResponse{Defaults: workDefaultsToProto(value.Defaults), Options: workStartOptionsToProto(value.Options)}, nil
}

// ChangeAgentWorkDefaults replaces an Application's Agent's Work defaults for
// the Application or its Application Owner.
func (h *WorkHandler) ChangeAgentWorkDefaults(ctx context.Context, request *agentpublicv1.ChangeAgentWorkDefaultsRequest) (*agentpublicv1.ChangeAgentWorkDefaultsResponse, error) {
	configurer, ok := currentConfigurer(ctx)
	if !ok {
		return nil, errNoOwner
	}
	value := request.GetDefaults()
	saved, err := h.application.ChangeAgentWorkDefaults(ctx, configurer, strings.TrimSpace(request.GetAgentId()), domain.WorkDefaults{
		StartupAgent: value.GetStartupAgent(), ModelConfigurationID: value.GetModelConfigurationId(), ThinkingLevel: value.GetThinkingLevel(),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.ChangeAgentWorkDefaultsResponse{Defaults: workDefaultsToProto(saved)}, nil
}

func workDefaultsToProto(value domain.WorkDefaults) *agentpublicv1.WorkDefaults {
	return &agentpublicv1.WorkDefaults{StartupAgent: value.StartupAgent, ModelConfigurationId: value.ModelConfigurationID, ThinkingLevel: value.ThinkingLevel}
}

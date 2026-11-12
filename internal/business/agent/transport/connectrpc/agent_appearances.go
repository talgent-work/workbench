package connectrpc

import (
	"context"
	"strings"

	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

func appearanceToProto(appearance domain.AgentAppearance) *agentpublicv1.AgentAppearance {
	return &agentpublicv1.AgentAppearance{Name: appearance.Name, Icon: &agentpublicv1.AgentIcon{Glyph: appearance.Icon.Glyph, Color: appearance.Icon.Color}}
}

func (h *AgentHandler) ResolveEmployeeAgents(ctx context.Context, request *agentprivatev1.ResolveEmployeeAgentsRequest) (*agentprivatev1.ResolveEmployeeAgentsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	results, err := h.application.Queries.ResolveAgents.ByEmployee(ctx, request.GetPurpose(), request.GetEmployeeIds())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ResolveEmployeeAgentsResponse{Agents: make([]*agentprivatev1.ResolvedEmployeeAgent, len(results))}
	for index, result := range results {
		resolved := &agentprivatev1.ResolvedEmployeeAgent{EmployeeId: result.Reference, Known: result.Known}
		if result.Known {
			resolved.AgentId, resolved.Appearance = result.AgentID, appearanceToProto(result.Appearance)
		}
		response.Agents[index] = resolved
	}
	return response, nil
}

func (h *AgentHandler) GetEmployeeAgent(ctx context.Context, request *agentprivatev1.GetEmployeeAgentRequest) (*agentprivatev1.GetEmployeeAgentResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	agent, err := h.application.Queries.ResolveAgents.OfEmployee(ctx, request.GetPurpose(), request.GetEmployeeId(), request.GetEmployeeDisplayName())
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentprivatev1.GetEmployeeAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
}

func (h *AgentHandler) ResolveWorkAgents(ctx context.Context, request *agentprivatev1.ResolveWorkAgentsRequest) (*agentprivatev1.ResolveWorkAgentsResponse, error) {
	if err := authenticateIntegration(h.token, requestHeader(ctx).Get("Authorization")); err != nil {
		return nil, err
	}
	results, err := h.application.Queries.ResolveAgents.ByWork(ctx, request.GetWorkIds())
	if err != nil {
		return nil, mapAgentError(err)
	}
	response := &agentprivatev1.ResolveWorkAgentsResponse{Agents: make([]*agentprivatev1.ResolvedWorkAgent, len(results))}
	for index, result := range results {
		resolved := &agentprivatev1.ResolvedWorkAgent{WorkId: result.Reference, Known: result.Known}
		if result.Known {
			resolved.Appearance = appearanceToProto(result.Appearance)
		}
		response.Agents[index] = resolved
	}
	return response, nil
}

// ChangeWorkAgentAppearance lets the signed-in person change the Work Agent
// that represents them, or, as Application Owner, an Application's Agent; the
// definition decides whether they may.
func (h *WorkHandler) ChangeWorkAgentAppearance(ctx context.Context, request *agentpublicv1.ChangeWorkAgentAppearanceRequest) (*agentpublicv1.ChangeWorkAgentAppearanceResponse, error) {
	agentID := strings.TrimSpace(request.GetAgentId())
	requester, err := h.definitionRequester(ctx, agentID, domain.ErrAgentAppearanceForbidden)
	if err != nil {
		return nil, err
	}
	appearance, err := h.application.Commands.ChangeAgentAppearance.Handle(ctx, command.ChangeAgentAppearance{
		AgentID: agentID, Requester: requester, Name: request.GetName(),
		Glyph: request.GetIcon().GetGlyph(), Color: request.GetIcon().GetColor(),
	})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.ChangeWorkAgentAppearanceResponse{Appearance: appearanceToProto(appearance)}, nil
}

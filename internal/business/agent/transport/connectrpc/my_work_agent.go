package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

var (
	errNoOwner  = connect.NewError(connect.CodeUnauthenticated, "agent: an authenticated person or Application is required")
	errNoPerson = connect.NewError(connect.CodeUnauthenticated, "agent: an authenticated person is required")
)

// GetMyWorkAgent shows the caller's own Agent, a person's Work Agent or an
// Application's Agent, without creating it.
func (h *WorkHandler) GetMyWorkAgent(ctx context.Context, _ *agentpublicv1.GetMyWorkAgentRequest) (*agentpublicv1.GetMyWorkAgentResponse, error) {
	configurer, ok := currentConfigurer(ctx)
	if !ok {
		return nil, errNoOwner
	}
	var (
		agent query.AgentAppearanceView
		err   error
	)
	if configurer.ApplicationID != "" {
		agent, err = h.application.OwnApplicationAgent(ctx, configurer.ApplicationID)
	} else {
		user, _ := authn.CurrentUser(ctx)
		agent, err = h.application.Queries.ResolveAgents.OfEmployee(ctx, domain.StandaloneWorkPurpose, user.ID, nameOf(user))
	}
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.GetMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
}

// EnsureMyWorkAgent returns the caller's own Agent, establishing it from
// product defaults when it has none yet: a person's named after them, an
// Application's after the Application.
func (h *WorkHandler) EnsureMyWorkAgent(ctx context.Context, _ *agentpublicv1.EnsureMyWorkAgentRequest) (*agentpublicv1.EnsureMyWorkAgentResponse, error) {
	configurer, ok := currentConfigurer(ctx)
	if !ok {
		return nil, errNoOwner
	}
	if configurer.ApplicationID != "" {
		agent, err := h.application.EnsureOwnApplicationAgent(ctx, configurer.ApplicationID)
		if err != nil {
			return nil, mapAgentError(err)
		}
		return &agentpublicv1.EnsureMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
	}
	user, _ := authn.CurrentUser(ctx)
	agent, err := h.application.EnsureEmployeeAgent(ctx, user.ID, nameOf(user))
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.EnsureMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
}

// nameOf is what a new Work Agent is named after: the person's display name,
// else their username.
func nameOf(user authn.AuthenticatedUser) string {
	if name := strings.TrimSpace(user.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(user.Username)
}

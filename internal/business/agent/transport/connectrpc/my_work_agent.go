package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	agentpublicv1 "github.com/talgent-work/contracts/gen/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

var errNoPerson = connect.NewError(connect.CodeUnauthenticated, "agent: an authenticated person is required")

// GetMyWorkAgent shows the signed-in person's Work Agent without creating it.
func (h *WorkHandler) GetMyWorkAgent(ctx context.Context, _ *agentpublicv1.GetMyWorkAgentRequest) (*agentpublicv1.GetMyWorkAgentResponse, error) {
	user, ok := authn.CurrentUser(ctx)
	if !ok || user.ID == "" {
		return nil, errNoPerson
	}
	agent, err := h.application.Queries.ResolveAgents.OfEmployee(ctx, domain.EmployeeWorkPurpose, user.ID, nameOf(user))
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.GetMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}, nil
}

// EnsureMyWorkAgent returns the signed-in person's Work Agent, establishing it
// from product defaults, named after them, when they have none yet.
func (h *WorkHandler) EnsureMyWorkAgent(ctx context.Context, _ *agentpublicv1.EnsureMyWorkAgentRequest) (*agentpublicv1.EnsureMyWorkAgentResponse, error) {
	user, ok := authn.CurrentUser(ctx)
	if !ok || user.ID == "" {
		return nil, errNoPerson
	}
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

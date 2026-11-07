package connectrpc

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

var errNoPerson = connect.NewError(connect.CodeUnauthenticated, errors.New("agent: an authenticated person is required"))

// GetMyWorkAgent shows the signed-in person's Work Agent without creating it.
func (h *WorkHandler) GetMyWorkAgent(ctx context.Context, _ *connect.Request[agentpublicv1.GetMyWorkAgentRequest]) (*connect.Response[agentpublicv1.GetMyWorkAgentResponse], error) {
	user, ok := authn.CurrentUser(ctx)
	if !ok || user.ID == "" {
		return nil, errNoPerson
	}
	agent, err := h.application.Queries.ResolveAgents.OfEmployee(ctx, domain.EmployeeWorkPurpose, user.ID, nameOf(user))
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.GetMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}), nil
}

// EnsureMyWorkAgent returns the signed-in person's Work Agent, establishing it
// from product defaults, named after them, when they have none yet.
func (h *WorkHandler) EnsureMyWorkAgent(ctx context.Context, _ *connect.Request[agentpublicv1.EnsureMyWorkAgentRequest]) (*connect.Response[agentpublicv1.EnsureMyWorkAgentResponse], error) {
	user, ok := authn.CurrentUser(ctx)
	if !ok || user.ID == "" {
		return nil, errNoPerson
	}
	agent, err := h.application.EnsureEmployeeAgent(ctx, user.ID, nameOf(user))
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.EnsureMyWorkAgentResponse{AgentId: agent.AgentID, Appearance: appearanceToProto(agent.Appearance)}), nil
}

// nameOf is what a new Work Agent is named after: the person's display name,
// else their username.
func nameOf(user authn.AuthenticatedUser) string {
	if name := strings.TrimSpace(user.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(user.Username)
}

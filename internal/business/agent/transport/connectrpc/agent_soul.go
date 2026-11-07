package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// GetAgentSoul reads an Agent's Soul for the signed-in person, who must be one
// of its Owners.
func (h *WorkHandler) GetAgentSoul(ctx context.Context, request *connect.Request[agentpublicv1.GetAgentSoulRequest]) (*connect.Response[agentpublicv1.GetAgentSoulResponse], error) {
	requester, ok := authn.CurrentUserID(ctx)
	if !ok || requester == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, domain.ErrSoulForbidden)
	}
	soul, err := h.application.Queries.GetAgentSoul.Handle(ctx, strings.TrimSpace(request.Msg.GetAgentId()), requester)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.GetAgentSoulResponse{Soul: soul}), nil
}

// ChangeAgentSoul replaces an Agent's Soul for the signed-in person, who must
// be one of its Owners.
func (h *WorkHandler) ChangeAgentSoul(ctx context.Context, request *connect.Request[agentpublicv1.ChangeAgentSoulRequest]) (*connect.Response[agentpublicv1.ChangeAgentSoulResponse], error) {
	requester, ok := authn.CurrentUserID(ctx)
	if !ok || requester == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, domain.ErrSoulForbidden)
	}
	soul, err := h.application.Commands.ChangeAgentSoul.Handle(ctx, command.ChangeAgentSoul{AgentID: strings.TrimSpace(request.Msg.GetAgentId()), Requester: requester, Soul: request.Msg.GetSoul()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return connect.NewResponse(&agentpublicv1.ChangeAgentSoulResponse{Soul: soul}), nil
}

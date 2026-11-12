package connectrpc

import (
	"context"
	"strings"

	"connectrpc.com/connect/v2"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// currentConfigurer is who the request speaks for when it configures an
// Agent: the signed-in person or the Application; an Agent configures none.
func currentConfigurer(ctx context.Context) (application.AgentConfigurer, bool) {
	principal, _ := authn.PrincipalFrom(ctx)
	if found, ok := principal.Application(); ok && found.ID != "" {
		return application.AgentConfigurer{ApplicationID: found.ID}, true
	}
	if _, ok := principal.Agent(); ok {
		return application.AgentConfigurer{}, false
	}
	person, ok := authn.CurrentUserID(ctx)
	return application.AgentConfigurer{PersonID: person}, ok && person != ""
}

// definitionRequester resolves who asks the addressed definition.
func (h *WorkHandler) definitionRequester(ctx context.Context, agentID string, forbidden error) (domain.DefinitionRequester, error) {
	configurer, ok := currentConfigurer(ctx)
	if !ok {
		return domain.DefinitionRequester{}, connect.NewError(connect.CodeUnauthenticated, forbidden.Error()).WithCause(forbidden)
	}
	requester, err := h.application.DefinitionRequester(ctx, configurer, agentID)
	if err != nil {
		return domain.DefinitionRequester{}, mapAgentError(err)
	}
	return requester, nil
}

// GetAgentSoul reads an Agent's Soul for one of its Owners, a person or the
// Application, or for an Application's Agent's Application Owner.
func (h *WorkHandler) GetAgentSoul(ctx context.Context, request *agentpublicv1.GetAgentSoulRequest) (*agentpublicv1.GetAgentSoulResponse, error) {
	agentID := strings.TrimSpace(request.GetAgentId())
	requester, err := h.definitionRequester(ctx, agentID, domain.ErrSoulForbidden)
	if err != nil {
		return nil, err
	}
	soul, err := h.application.Queries.GetAgentSoul.Handle(ctx, agentID, requester)
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.GetAgentSoulResponse{Soul: soul}, nil
}

// ChangeAgentSoul replaces an Agent's Soul for one of its Owners, a person or
// the Application, or for an Application's Agent's Application Owner.
func (h *WorkHandler) ChangeAgentSoul(ctx context.Context, request *agentpublicv1.ChangeAgentSoulRequest) (*agentpublicv1.ChangeAgentSoulResponse, error) {
	agentID := strings.TrimSpace(request.GetAgentId())
	requester, err := h.definitionRequester(ctx, agentID, domain.ErrSoulForbidden)
	if err != nil {
		return nil, err
	}
	soul, err := h.application.Commands.ChangeAgentSoul.Handle(ctx, command.ChangeAgentSoul{AgentID: agentID, Requester: requester, Soul: request.GetSoul()})
	if err != nil {
		return nil, mapAgentError(err)
	}
	return &agentpublicv1.ChangeAgentSoulResponse{Soul: soul}, nil
}

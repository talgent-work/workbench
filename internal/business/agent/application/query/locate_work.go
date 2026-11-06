package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type LocateWork struct {
	AgentID   string
	SubjectID string
	Subject   domain.WorkSubject
}

type LocateWorkHandler struct {
	agents        domain.AgentDefinitionRepository
	lookup        domain.WorkLookupPort
	authorization access.WorkAuthorizer
}

func NewLocateWorkHandler(agents domain.AgentDefinitionRepository, lookup domain.WorkLookupPort, authorization access.WorkAuthorizer) *LocateWorkHandler {
	return &LocateWorkHandler{agents: agents, lookup: lookup, authorization: authorization}
}

func (h *LocateWorkHandler) Handle(ctx context.Context, request LocateWork) (string, bool, error) {
	agent, err := h.agents.Get(ctx, request.AgentID)
	if err != nil {
		return "", false, err
	}
	// Only a Subject locates a Work; one without a Subject is found by its
	// identity.
	if !request.Subject.Present() {
		return "", false, domain.ErrInvalidWorkSubject
	}
	session, err := agent.ReuseWork(ctx, request.Subject, h.lookup)
	if err != nil {
		return "", false, err
	}
	if session == nil {
		return "", false, nil
	}
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, session.ID); err != nil {
		return "", false, err
	}
	return session.ID, true, nil
}

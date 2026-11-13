package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// LocateWork finds the definition's Work of a Work Key within one Work
// Caller.
type LocateWork struct {
	AgentID    string
	SubjectID  string
	WorkKey    domain.WorkKey
	WorkCaller domain.WorkCaller
}

type LocateWorkHandler struct {
	agents        domain.AgentDefinitionRepository
	works         domain.AgentWorkRepository
	authorization access.WorkAuthorizer
}

func NewLocateWorkHandler(agents domain.AgentDefinitionRepository, works domain.AgentWorkRepository, authorization access.WorkAuthorizer) *LocateWorkHandler {
	return &LocateWorkHandler{agents: agents, works: works, authorization: authorization}
}

func (h *LocateWorkHandler) Handle(ctx context.Context, request LocateWork) (string, bool, error) {
	agent, err := h.agents.Get(ctx, request.AgentID)
	if err != nil {
		return "", false, err
	}
	// Only a Work Key locates a Work (Work Key lookup); one without a key is
	// found by its identity. A Work of another Work Caller is not found.
	if !request.WorkKey.Present() {
		return "", false, domain.ErrInvalidWorkKey
	}
	if err := request.WorkKey.Validate(); err != nil {
		return "", false, err
	}
	if err := request.WorkCaller.Validate(); err != nil {
		return "", false, err
	}
	session, err := h.works.FindByWorkKey(ctx, agent.ID, request.WorkKey)
	if err != nil {
		return "", false, err
	}
	if session == nil || session.WorkCaller != request.WorkCaller {
		return "", false, nil
	}
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, session.ID); err != nil {
		return "", false, err
	}
	return session.ID, true, nil
}

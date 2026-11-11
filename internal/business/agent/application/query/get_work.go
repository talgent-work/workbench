package query

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type AgentWork struct {
	Progress      *WorkProgress
	AgentName     string
	StopReason    domain.StopReason
	Waiting       bool
	Version       int
	StopRequestID string
	ID            string
	AgentID       string
	State         domain.WorkState
	Capabilities  domain.CapabilitySnapshot
	// ProjectID is the Work's Project; empty for a Work without one.
	ProjectID               string
	HarnessSessionReference string
	RequiredResponse        bool
	FailureReason           string
}

type GetWork struct {
	SubjectID string
	WorkID    string
}

type RequiredResponseReader func(context.Context, string) (bool, error)

type GetWorkHandler struct {
	requiredResponse RequiredResponseReader
	definitions      domain.AgentDefinitionRepository
	access           access.WorkAuthorizer
	repository       domain.AgentWorkRepository
	assemble         func(*domain.AgentWork, *domain.AgentDefinition) AgentWork
}

func NewGetWorkHandler(definitions domain.AgentDefinitionRepository, repository domain.AgentWorkRepository, authorization access.WorkAuthorizer, assemble func(*domain.AgentWork, *domain.AgentDefinition) AgentWork, requiredResponse RequiredResponseReader) *GetWorkHandler {
	return &GetWorkHandler{definitions: definitions, repository: repository, access: authorization, assemble: assemble, requiredResponse: requiredResponse}
}

func (h *GetWorkHandler) Handle(ctx context.Context, request GetWork) (AgentWork, error) {
	if err := h.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return AgentWork{}, err
	}
	session, err := h.repository.Get(ctx, request.WorkID)
	if err != nil {
		return AgentWork{}, err
	}
	definition, err := h.definitions.Get(ctx, session.AgentID)
	if err != nil {
		return AgentWork{}, err
	}
	result := h.assemble(session, definition)
	if session.State == domain.WorkWorking {
		result.RequiredResponse, err = h.requiredResponse(ctx, session.ID)
		if err != nil {
			return AgentWork{}, err
		}
	}
	return result, nil
}

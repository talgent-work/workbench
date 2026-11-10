package query

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// VerifyWorkCredential names the credential to verify; RequireActing admits
// it only while its Work acts, for a caller admitting the Work's actions.
type VerifyWorkCredential struct {
	WorkID        string
	Credential    string
	RequireActing bool
}

// WorkIdentity is the verified Work: its definition, principal, optional
// Project and Subject, and its Session Workspace as its sandbox sees it, nil
// for a Work that mounts none, such as a Work on a Managed Computer.
type WorkIdentity struct {
	AgentName string
	WorkID    string
	AgentID   string
	SubjectID string
	Workspace *domain.WorkspaceLayout
	ProjectID string
	Subject   domain.WorkSubject
	Purpose   string
}

type VerifyWorkCredentialHandler struct {
	definitions domain.AgentDefinitionRepository
	sessions    domain.AgentWorkRepository
	credentials ExecutionQueryRepository
	assemble    func(*domain.AgentWork, *domain.AgentDefinition, string) WorkIdentity
	acting      *AdmitAgentActionHandler
}

func NewVerifyWorkCredentialHandler(definitions domain.AgentDefinitionRepository, sessions domain.AgentWorkRepository, credentials ExecutionQueryRepository, assemble func(*domain.AgentWork, *domain.AgentDefinition, string) WorkIdentity, acting *AdmitAgentActionHandler) *VerifyWorkCredentialHandler {
	return &VerifyWorkCredentialHandler{definitions: definitions, sessions: sessions, credentials: credentials, assemble: assemble, acting: acting}
}

func (h *VerifyWorkCredentialHandler) Handle(ctx context.Context, request VerifyWorkCredential) (WorkIdentity, error) {
	if err := h.credentials.VerifyWorkCredential(ctx, request.WorkID, request.Credential); err != nil {
		return WorkIdentity{}, errors.Join(access.ErrAccessDenied, err)
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return WorkIdentity{}, err
	}
	definition, err := h.definitions.Get(ctx, session.AgentID)
	if err != nil {
		return WorkIdentity{}, err
	}
	subject := definition.WorkPrincipal()
	if subject == "" {
		return WorkIdentity{}, access.ErrAccessDenied
	}
	identity := h.assemble(session, definition, subject)
	if request.RequireActing {
		if err := h.acting.Handle(ctx, AdmitAgentAction{WorkID: session.ID}); err != nil {
			return WorkIdentity{}, err
		}
	}
	return identity, nil
}

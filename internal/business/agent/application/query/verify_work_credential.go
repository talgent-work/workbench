package query

import (
	"context"
	"errors"
	"time"

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

// actingWait bounds how long an action waits for a delivered reply to resolve
// its Work's required response, polling at actingPoll; an Agent may call a
// tool the moment it receives the reply, before the response is recorded.
const (
	actingWait = 2 * time.Second
	actingPoll = 20 * time.Millisecond
)

type VerifyWorkCredentialHandler struct {
	definitions      domain.AgentDefinitionRepository
	sessions         domain.AgentWorkRepository
	credentials      ExecutionQueryRepository
	assemble         func(*domain.AgentWork, *domain.AgentDefinition, string) WorkIdentity
	requiredResponse RequiredResponseReader
}

func NewVerifyWorkCredentialHandler(definitions domain.AgentDefinitionRepository, sessions domain.AgentWorkRepository, credentials ExecutionQueryRepository, assemble func(*domain.AgentWork, *domain.AgentDefinition, string) WorkIdentity, requiredResponse RequiredResponseReader) *VerifyWorkCredentialHandler {
	return &VerifyWorkCredentialHandler{definitions: definitions, sessions: sessions, credentials: credentials, assemble: assemble, requiredResponse: requiredResponse}
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
		if err := h.awaitActing(ctx, session); err != nil {
			return WorkIdentity{}, err
		}
	}
	return identity, nil
}

// awaitActing returns once the Work acts, or refuses when it is not Working or
// its required response stays unresolved through actingWait.
func (h *VerifyWorkCredentialHandler) awaitActing(ctx context.Context, session *domain.AgentWork) error {
	deadline := time.Now().Add(actingWait)
	for {
		if session.State != domain.WorkWorking {
			return domain.ErrWorkNotActing
		}
		awaiting, err := h.requiredResponse(ctx, session.ID)
		if err != nil {
			return err
		}
		if session.Acts(awaiting) {
			return nil
		}
		if time.Now().Add(actingPoll).After(deadline) {
			return domain.ErrWorkNotActing
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(actingPoll):
		}
		if session, err = h.sessions.Get(ctx, session.ID); err != nil {
			return err
		}
	}
}

package query

import (
	"context"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// WorkView is a read-only view of Session and retained request facts.
// Its display state is never persisted as a second lifecycle.
type WorkView struct {
	Version  int
	Progress *WorkProgress
	// SubAgents are read under Work Visibility, never as collaboration facts.
	SubAgents []SubAgent
	WorkModel domain.WorkModelSelection
	// ModelOption is the retained Model Configuration as currently provided;
	// nil when it is withdrawn.
	ModelOption     *WorkModelOption
	AllowedActions  []string
	StartupProgress *StartupProgress
	StartupRequest  *WorkStartupRequest
	// Subject is the one business object the Work is about; Agent does not
	// interpret it. ProjectID is the Work's Project.
	Subject   domain.WorkSubject
	ProjectID string
	WorkID    string
	// AgentOwner is the Work's Agent Owner, its definition's Owner; StartedBy
	// is who started it, a fact.
	AgentOwner, StartedBy                string
	Visibility                           domain.WorkVisibility
	WorkShares                           []string
	State, StateReason, DiagnosticReason string
	StopRequestID                        string
	CreatedAt, UpdatedAt                 time.Time
	// Waiting reports a Work whose user owes the answer to a question, even
	// after it stopped.
	Waiting                       bool
	ArchivePending, ResumePending bool
	// ResumeOnReconnection reports a Stopped Work whose Resume is requested
	// once more when its Managed Computer reconnects, its input meanwhile
	// kept (ReachResumeDue).
	ResumeOnReconnection bool
	// DeskWaiting reports a start waiting in its Computer's line.
	DeskWaiting bool
	// DisconnectedSince is the origin of the Work's current Disconnection;
	// zero while its execution environment is observed.
	DisconnectedSince time.Time
	// PermissionMode is a Managed Work's Harness permission mode; empty for a
	// Cloud Work. Sandbox reports that it runs in Claude Code's sandbox.
	PermissionMode domain.PermissionMode
	Sandbox        bool
	// ComputerID is the Computer the Work runs on for its life.
	ComputerID string
}

// WorkStartupRequest projects retained input and fixed capability identities.
// Credentials, Plugin configuration and system prompts are intentionally absent.
type WorkStartupRequest struct {
	RequestID, InitialMessage, InitialThinkingLevel, StartupAgent string
	Plugins                                                       []string
	Repositories                                                  []WorkStartupRepository
}

// WorkStartupRepository is a retained selection with its checkout outcome once
// workspace preparation has ended ("checked_out" or "not_checked_out").
type WorkStartupRepository struct{ RepositoryID, Name, BaseBranch, CheckoutStatus, CheckoutReason string }

// WorkQueryRepository groups Work views and attribution.
type WorkQueryRepository interface {
	WorkViewReader
	WorkListReader
	WorkOriginQueryRepository
}

type WorkViewReader interface {
	GetWorkView(context.Context, string) (WorkView, error)
}

type GetWorkView struct{ ActorUserID, WorkID string }

type GetWorkViewHandler struct {
	authorization access.WorkAuthorizer
	reader        WorkViewReader
}

func NewGetWorkViewHandler(authorization access.WorkAuthorizer, reader WorkViewReader) *GetWorkViewHandler {
	return &GetWorkViewHandler{authorization: authorization, reader: reader}
}

// Handle reads the owner's own Work. Read-side affordances mirror work
// controls; commands remain authoritative and recheck lifecycle/admission.
func (h *GetWorkViewHandler) Handle(ctx context.Context, request GetWorkView) (WorkView, error) {
	if strings.TrimSpace(request.WorkID) == "" {
		return WorkView{}, domain.ErrInvalidWork
	}
	if err := h.authorization.AuthorizeWork(ctx, request.ActorUserID, request.WorkID); err != nil {
		return WorkView{}, err
	}
	value, err := h.reader.GetWorkView(ctx, request.WorkID)
	if err != nil {
		return WorkView{}, err
	}
	if value.ArchivePending || value.ResumePending {
		return value, nil
	}
	switch value.State {
	case "starting":
		// A stop or close withdraws a start still waiting for room.
		if value.DeskWaiting {
			value.AllowedActions = []string{"stop", "archive"}
		}
	case "working":
		value.AllowedActions = []string{"stop"}
		// Only the person who started a Managed Work switches its mode.
		if value.PermissionMode != "" && domain.AdmitPermissionModeRequester(value.StartedBy, request.ActorUserID) == nil {
			value.AllowedActions = append(value.AllowedActions, "set_permission_mode")
		}
	case "stopping":
		value.AllowedActions = disconnectedActions(value)
	case "stopped":
		value.AllowedActions = []string{"resume", "archive"}
	case "failed":
		value.AllowedActions = []string{"archive", "restart"}
	}
	return value, nil
}

// disconnectedActions are what a Stopping Work admits at once during a
// Disconnection: its stop or close ends the run without reaching its
// process, a Managed machine's fence vouching for it and a Cloud sandbox
// being reclaimed. A Working one is offered only its stop, which is always
// admitted then; closing it directly may still be refused for input or a
// start it owes, which this view does not read, and once stopped it is
// offered its archive.
func disconnectedActions(value WorkView) []string {
	if value.DisconnectedSince.IsZero() {
		return nil
	}
	return []string{"stop", "archive"}
}

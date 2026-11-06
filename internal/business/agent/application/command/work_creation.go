package command

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var (
	ErrWorkCreationNotFound = errors.New("agent: Work creation request not found")
	ErrWorkCreationConflict = errors.New("agent: Work creation request has different immutable content")
	ErrWorkCreationCanceled = errors.New("agent: Work creation request was cancelled")
)

// WorkCreationRequest is application recovery data for the original explicit
// request. It is not a Session, an execution Aggregate or a capacity queue. A
// request cancelled before Agent received it carries no Creation.
type WorkCreationRequest struct {
	ID, WorkID                 string
	Creation                   CreateWork
	BindingConfirmed, Canceled bool
}

func NewWorkCreationRequest(creation CreateWork) (WorkCreationRequest, error) {
	if strings.TrimSpace(creation.SubjectID) == "" {
		return WorkCreationRequest{}, access.ErrAccessDenied
	}
	if creation.InitialExecution == nil || creation.InitialExecution.WorkID != "" {
		return WorkCreationRequest{}, domain.ErrInvalidWork
	}
	initial := *creation.InitialExecution
	if err := validateInitialPreparation(initial); err != nil {
		return WorkCreationRequest{}, err
	}
	initial.SubjectID = creation.SubjectID
	initial.CreationRequestID = initial.RequestID
	initial.WorkspaceReference = creation.WorkspaceReference
	creation.InitialExecution = &initial
	return WorkCreationRequest{ID: initial.RequestID, Creation: creation}, nil
}

type WorkCreationReader interface {
	GetWorkCreation(context.Context, string) (WorkCreationRequest, error)
}

// CancelWorkCreation serializes with actual registration and fences an
// identity Agent has not yet received. If registration won, its result retains
// the Work, and the request is not cancelled.
type WorkCreationRequests interface {
	WorkCreationReader
	RetainWorkCreation(context.Context, WorkCreationRequest) (WorkCreationRequest, error)
	CancelWorkCreation(context.Context, string) (WorkCreationRequest, error)
	ConfirmWorkCreation(context.Context, string, string) error
	CompleteWorkCreation(context.Context, string) error
}

package query

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
)

var ErrInvalidStartRequest = errors.New("agent: invalid start request")

// StartKind names the admitted operation of a start request.
type StartKind string

const (
	StartKindStart   StartKind = "start"
	StartKindResume  StartKind = "resume"
	StartKindRestart StartKind = "restart"
)

// AdmittedStart is retained request correlation. It identifies which start
// request was admitted for a Work, never a process run.
type AdmittedStart struct {
	WorkID string
	Kind   StartKind
}

type StartAdmissionReader interface {
	FindAdmittedStart(context.Context, string) (AdmittedStart, bool, error)
}

type GetStartAdmission struct{ SubjectID, WorkID, RequestID string }

type GetStartAdmissionHandler struct {
	reader        StartAdmissionReader
	authorization access.WorkAuthorizer
}

func NewGetStartAdmissionHandler(reader StartAdmissionReader, authorization access.WorkAuthorizer) *GetStartAdmissionHandler {
	return &GetStartAdmissionHandler{reader: reader, authorization: authorization}
}

// Handle reads the retained admission, independently of the current Work state.
// Absence describes this observation only; it does not authorize automatic resubmission.
func (h *GetStartAdmissionHandler) Handle(ctx context.Context, request GetStartAdmission) (AdmittedStart, bool, error) {
	if err := h.authorization.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return AdmittedStart{}, false, err
	}
	if uuid.Validate(request.RequestID) != nil {
		return AdmittedStart{}, false, ErrInvalidStartRequest
	}
	admitted, found, err := h.reader.FindAdmittedStart(ctx, request.RequestID)
	if err != nil {
		return AdmittedStart{}, false, err
	}
	if !found || admitted.WorkID != request.WorkID {
		return AdmittedStart{}, false, nil
	}
	return admitted, true, nil
}

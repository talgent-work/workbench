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

// StartRequest is the single record of one caller request identity for a
// Work creation, Resume or Restart: which operation it named, the Work it
// started, a digest of its content and whether it was cancelled. It
// identifies a caller's request, never a process run.
type StartRequest struct {
	ID   string
	Kind StartKind
	// WorkID is empty for a creation cancelled or refused before it created
	// a Work.
	WorkID string
	// Fingerprint is the content digest of the request (Request
	// fingerprint); empty for an identity cancelled before Agent received it.
	Fingerprint string
	Canceled    bool
}

// Admitted reports whether the request started its Work.
func (r StartRequest) Admitted() bool { return !r.Canceled && r.WorkID != "" }

// OfCreation reports whether the record is of a creation: one that created or
// reused its Work, or an identity cancelled before any Work.
func (r StartRequest) OfCreation() bool {
	return r.Kind == StartKindStart || r.Canceled && r.WorkID == ""
}

// SameContent reports whether the record was made for this content. A record
// without a fingerprint was cancelled before Agent received its request, and
// holds no content to compare.
func (r StartRequest) SameContent(fingerprint string) bool {
	return r.Fingerprint == "" || r.Fingerprint == fingerprint
}

// AdmittedStart identifies which start request was admitted for a Work.
type AdmittedStart struct {
	WorkID string
	Kind   StartKind
}

type StartRequestReader interface {
	FindStartRequest(context.Context, string) (StartRequest, bool, error)
}

type GetStartAdmission struct{ SubjectID, WorkID, RequestID string }

type GetStartAdmissionHandler struct {
	reader        StartRequestReader
	authorization access.WorkAuthorizer
}

func NewGetStartAdmissionHandler(reader StartRequestReader, authorization access.WorkAuthorizer) *GetStartAdmissionHandler {
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
	record, found, err := h.reader.FindStartRequest(ctx, request.RequestID)
	if err != nil {
		return AdmittedStart{}, false, err
	}
	if !found || !record.Admitted() || record.WorkID != request.WorkID {
		return AdmittedStart{}, false, nil
	}
	return AdmittedStart{WorkID: record.WorkID, Kind: record.Kind}, true, nil
}

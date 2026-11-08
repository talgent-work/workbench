package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// EndWorkExecution ends the Work's Harness process for the caller of its
// start request. It never closes the conversation. A start request is not
// cancelled before admission: no request record is kept, and a start admitted
// later is ended by a later request.
type EndWorkExecution struct {
	SubjectID      string
	WorkID         string
	StartRequestID string
	RequestID      string
}

type EndWorkExecutionHandler struct {
	control sessionControl
}

func NewEndWorkExecutionHandler(sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer) *EndWorkExecutionHandler {
	return &EndWorkExecutionHandler{control: sessionControl{sessions: sessions, access: authorization}}
}

func (h *EndWorkExecutionHandler) Handle(ctx context.Context, request EndWorkExecution) error {
	if err := h.control.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return err
	}
	if uuid.Validate(request.StartRequestID) != nil {
		return ErrInvalidStartRequest
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return domain.ErrInvalidWork
	}
	return h.control.apply(ctx, WorkControl{SubjectID: request.SubjectID, WorkID: request.WorkID, RequestID: request.RequestID}, func(_ context.Context, work *domain.AgentWork, requestID string) error {
		return work.RequestExecutionEnd(requestID)
	})
}

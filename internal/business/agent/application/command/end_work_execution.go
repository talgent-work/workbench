package command

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// EndWorkExecution ends the Work's Harness process for a caller whose start
// request was admitted, or prevents that request's admission when it is still
// pending. It never closes the conversation.
type EndWorkExecution struct {
	SubjectID      string
	WorkID         string
	StartRequestID string
	RequestID      string
}

type EndWorkExecutionHandler struct {
	control sessionControl
	starts  AgentWorkRepository
}

func NewEndWorkExecutionHandler(sessions AgentWorkRepository, authorization access.WorkAuthorizer, execution domain.ExecutionPort) *EndWorkExecutionHandler {
	return &EndWorkExecutionHandler{control: sessionControl{sessions: sessions, access: authorization, execution: execution}, starts: sessions}
}

func (h *EndWorkExecutionHandler) Handle(ctx context.Context, request EndWorkExecution) error {
	if err := h.control.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return err
	}
	if uuid.Validate(request.StartRequestID) != nil {
		return query.ErrInvalidStartRequest
	}
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 {
		return domain.ErrInvalidWork
	}
	canceled, err := h.starts.CancelStart(ctx, request.WorkID, request.StartRequestID)
	if err != nil || canceled {
		return err
	}
	return h.control.apply(ctx, WorkControl{SubjectID: request.SubjectID, WorkID: request.WorkID, RequestID: request.RequestID}, (*domain.AgentWork).RequestExecutionEnd)
}

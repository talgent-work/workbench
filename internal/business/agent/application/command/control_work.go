package command

import (
	"context"
	"errors"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type WorkControl struct {
	SubjectID string
	WorkID    string
	RequestID string
	// ExpectedVersion applies the control only to the Work version the caller observed.
	ExpectedVersion *int
}

// sessionControl applies one owner control to a Work. The WorkAuthorizer
// admits the requester by the Agent Owner rule on the Work's definition.
type sessionControl struct {
	sessions   domain.AgentWorkRepository
	access     access.WorkAuthorizer
	executions domain.AgentExecutionRepository
}

func (h sessionControl) apply(ctx context.Context, request WorkControl, change func(context.Context, *domain.AgentWork, string) error) error {
	if err := h.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return err
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return err
	}
	if request.ExpectedVersion != nil && *request.ExpectedVersion != session.Version {
		return domain.ErrConcurrentModification
	}
	if err := change(ctx, session, request.RequestID); err != nil {
		return err
	}
	return h.sessions.Save(ctx, session)
}

type StopWorkHandler struct{ control sessionControl }

func NewStopWorkHandler(sessions domain.AgentWorkRepository, access access.WorkAuthorizer, executions domain.AgentExecutionRepository) *StopWorkHandler {
	return &StopWorkHandler{control: sessionControl{sessions: sessions, access: access, executions: executions}}
}

// Handle stops the Work, withdrawing a start that still waits for its Desk.
func (h *StopWorkHandler) Handle(ctx context.Context, request WorkControl) error {
	return h.control.apply(ctx, request, func(ctx context.Context, work *domain.AgentWork, requestID string) error {
		queued, err := startQueued(ctx, h.control.executions, work)
		if err != nil {
			return err
		}
		return work.Stop(requestID, queued)
	})
}

type DeliverWorkInput struct {
	SubjectID string
	WorkID    string
	Input     ExecutionInput
}

type DeliverWorkInputHandler struct {
	initial  InitialExecutionReader
	sessions domain.AgentWorkRepository
	access   access.WorkAuthorizer
	inputs   WorkInputs
}

func NewDeliverWorkInputHandler(sessions domain.AgentWorkRepository, access access.WorkAuthorizer, inputs WorkInputs, initial InitialExecutionReader) *DeliverWorkInputHandler {
	return &DeliverWorkInputHandler{sessions: sessions, access: access, inputs: inputs, initial: initial}
}

// Deliver accepts the input and reports how the Work's buffer received it,
// so a caller retrying a delivery learns when the input already settled.
func (h *DeliverWorkInputHandler) Deliver(ctx context.Context, request DeliverWorkInput) (InputHandOff, error) {
	if err := h.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return "", err
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return "", err
	}
	if err := session.AdmitInput(); err != nil {
		return "", err
	}
	if err := request.Input.Validate(); err != nil {
		return "", err
	}
	initial, err := h.initial.Get(ctx, request.WorkID)
	if err != nil && !errors.Is(err, ErrInitialExecutionNotFound) {
		return "", err
	}
	// A rejected initial preparation failed its start; once that start was
	// ended, a later running start accepts ordinary input.
	if err == nil && !initial.InputBuffered && !(initial.Rejected && (initial.RejectionApplied || initial.CleanupConfirmed)) {
		return "", ErrInitialExecutionPending
	}
	return h.inputs.AcceptInput(ctx, request.WorkID, request.Input)
}

type RespondWorkInteraction struct {
	SubjectID string
	WorkID    string
	Response  InteractionResponse
}

type RespondWorkInteractionHandler struct {
	sessions domain.AgentWorkRepository
	access   access.WorkAuthorizer
	inputs   WorkInputs
}

func NewRespondWorkInteractionHandler(sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, inputs WorkInputs) *RespondWorkInteractionHandler {
	return &RespondWorkInteractionHandler{sessions: sessions, access: authorization, inputs: inputs}
}

func (h *RespondWorkInteractionHandler) Handle(ctx context.Context, request RespondWorkInteraction) error {
	if err := h.access.AuthorizeWork(ctx, request.SubjectID, request.WorkID); err != nil {
		return err
	}
	if strings.TrimSpace(request.Response.InteractionID) == "" {
		return domain.ErrInvalidWork
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return err
	}
	if err := session.AdmitInput(); err != nil {
		return err
	}
	return h.inputs.AcceptResponse(ctx, request.WorkID, request.Response)
}

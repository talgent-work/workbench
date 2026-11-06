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
	sessions  domain.AgentWorkRepository
	access    access.WorkAuthorizer
	execution ExecutionRequestsFactory
}

func (h sessionControl) apply(ctx context.Context, request WorkControl, change func(*domain.AgentWork, context.Context, string, domain.ExecutionPort) error) error {
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
	requests := h.execution()
	if err := change(session, ctx, request.RequestID, requests); err != nil {
		return err
	}
	if err := h.sessions.Save(ctx, session); err != nil {
		return err
	}
	return requests.Send(ctx)
}

type StopWorkHandler struct{ control sessionControl }

func NewStopWorkHandler(sessions domain.AgentWorkRepository, access access.WorkAuthorizer, execution ExecutionRequestsFactory) *StopWorkHandler {
	return &StopWorkHandler{control: sessionControl{sessions: sessions, access: access, execution: execution}}
}
func (h *StopWorkHandler) Handle(ctx context.Context, request WorkControl) error {
	return h.control.apply(ctx, request, (*domain.AgentWork).Stop)
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

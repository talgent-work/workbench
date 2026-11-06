package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
)

// CreateRequestedWorkHandler creates at most one Work per caller request
// identity under the application's creation-request record, then hands the
// first input to initial preparation. A repeated request returns its original
// outcome; a request the caller cancelled creates nothing. The caller owns the
// identity, its admission and its recovery.
type CreateRequestedWorkHandler struct {
	requests WorkCreationRequests
	create   *CreateWorkHandler
	prepare  *PrepareWorkExecutionHandler
}

func NewCreateRequestedWorkHandler(requests WorkCreationRequests, create *CreateWorkHandler, prepare *PrepareWorkExecutionHandler) *CreateRequestedWorkHandler {
	return &CreateRequestedWorkHandler{requests: requests, create: create, prepare: prepare}
}

func (h *CreateRequestedWorkHandler) Handle(ctx context.Context, creation CreateWork) (string, error) {
	request, err := NewWorkCreationRequest(creation)
	if err != nil {
		return "", err
	}
	request, err = h.requests.RetainWorkCreation(ctx, request)
	if err != nil {
		return "", err
	}
	return h.advance(ctx, request)
}

// Recover continues only the original request. Once initial preparation owns
// its durable receipt, the existing initial recovery owns further progress.
func (h *CreateRequestedWorkHandler) Recover(ctx context.Context, id string) error {
	request, err := h.requests.GetWorkCreation(ctx, id)
	if err != nil {
		return err
	}
	_, err = h.advance(ctx, request)
	if errors.Is(err, ErrWorkCreationCanceled) {
		return nil
	}
	return err
}

// Cancel settles an unanswered request identity for its caller: it returns the
// Work the request already created, or fences the identity so a delayed
// repeat creates nothing.
func (h *CreateRequestedWorkHandler) Cancel(ctx context.Context, id string) (string, error) {
	canceled, err := h.requests.CancelWorkCreation(ctx, id)
	if err != nil {
		return "", err
	}
	if canceled.WorkID == "" {
		return "", nil
	}
	return h.confirm(ctx, canceled)
}

// Outcome reports a request identity's settled answer without advancing it:
// its Work, its definitive cancellation, or neither while still pending.
func (h *CreateRequestedWorkHandler) Outcome(ctx context.Context, id string) (WorkCreationRequest, error) {
	return h.requests.GetWorkCreation(ctx, id)
}

func (h *CreateRequestedWorkHandler) advance(ctx context.Context, request WorkCreationRequest) (string, error) {
	if request.Canceled {
		return "", ErrWorkCreationCanceled
	}
	if request.BindingConfirmed {
		return h.prepareInitial(ctx, request)
	}
	if request.WorkID == "" {
		id, err := h.create.Handle(ctx, request.Creation)
		if err != nil {
			if definitiveCreationFailure(err) {
				return h.reject(ctx, request, err)
			}
			return "", err
		}
		request.WorkID = id
	}
	return h.confirm(ctx, request)
}

func (h *CreateRequestedWorkHandler) confirm(ctx context.Context, request WorkCreationRequest) (string, error) {
	if err := h.requests.ConfirmWorkCreation(ctx, request.ID, request.WorkID); err != nil {
		return "", err
	}
	return h.prepareInitial(ctx, request)
}

func (h *CreateRequestedWorkHandler) prepareInitial(ctx context.Context, request WorkCreationRequest) (string, error) {
	input := *request.Creation.InitialExecution
	input.WorkID = request.WorkID
	workID, err := h.prepare.Handle(ctx, input)
	// Pending initial delivery, rejected admission and its cleanup are retained
	// by the initial receipt. Uncertain writes leave this handoff discoverable.
	if err == nil || errors.Is(err, ErrInitialExecutionRejected) || errors.Is(err, ErrInitialExecutionConflict) || errors.Is(err, ErrPreparationRejected) || errors.Is(err, domain.ErrCapacityUnavailable) || domain.RefusedByComputer(err) || errors.Is(err, domain.ErrWorkTransition) {
		if completeErr := h.requests.CompleteWorkCreation(ctx, request.ID); completeErr != nil {
			return workID, errors.Join(err, completeErr)
		}
	}
	return workID, err
}

// reject fences the identity after a definitive refusal, so repeating it
// answers cancelled rather than retrying, unless a Work already won.
func (h *CreateRequestedWorkHandler) reject(ctx context.Context, request WorkCreationRequest, cause error) (string, error) {
	canceled, err := h.requests.CancelWorkCreation(ctx, request.ID)
	if err != nil {
		return "", errors.Join(cause, err)
	}
	if canceled.WorkID != "" {
		return h.confirm(ctx, canceled)
	}
	return "", errors.Join(cause, ErrWorkCreationCanceled)
}

func definitiveCreationFailure(err error) bool {
	return errors.Is(err, domain.ErrCapacityUnavailable) || domain.RefusedByComputer(err) || errors.Is(err, domain.ErrModelConfigurationWithdrawn) || errors.Is(err, pluginconfig.ErrInvalidConfiguration) || errors.Is(err, access.ErrAccessDenied) || errors.Is(err, ErrWorkCreationCanceled) || errors.Is(err, ErrWorkCreationConflict) || errors.Is(err, domain.ErrAgentDefinitionNotFound) || errors.Is(err, domain.ErrInvalidWork) || errors.Is(err, domain.ErrInvalidWorkSubject) || errors.Is(err, domain.ErrInvalidCapabilities)
}

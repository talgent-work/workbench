package command

import (
	"context"
	"errors"
	"reflect"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type PrepareWorkExecutionHandler struct {
	models        query.ModelConfigurations
	requests      InitialExecutionRequests
	sessions      domain.AgentWorkRepository
	authorization access.WorkAuthorizer
}

func NewPrepareWorkExecutionHandler(requests InitialExecutionRequests, sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, models query.ModelConfigurations) *PrepareWorkExecutionHandler {
	return &PrepareWorkExecutionHandler{models: models, requests: requests, sessions: sessions, authorization: authorization}
}

func (h *PrepareWorkExecutionHandler) Handle(ctx context.Context, input PrepareWorkExecution) (string, error) {
	if err := h.authorization.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return "", err
	}
	if err := validateInitialPreparation(input); err != nil {
		return "", err
	}
	session, err := h.sessions.Get(ctx, input.WorkID)
	if err != nil {
		return "", err
	}
	if err := session.AdmitPreparation(); err != nil {
		return "", err
	}
	if err := validateInitialThinkingLevel(h.models, session.WorkModel, input.ThinkingLevel); err != nil {
		return "", err
	}
	request, err := h.requests.Get(ctx, input.WorkID)
	if err != nil {
		return "", err
	}
	// A repeated work start after response loss reuses retained identities
	// only when its immutable preparation and input content still match.
	matched := input
	matched.RequestID, matched.Input.ID = request.RequestID, request.Input.ID
	// The creation correlation belongs to the retained original request; a
	// caller retrying the public preparation operation never selects it.
	matched.WorkspaceReference = request.WorkspaceReference
	if !reflect.DeepEqual(request.PrepareWorkExecution, matched) {
		return "", ErrInitialExecutionConflict
	}
	return h.advance(ctx, request)
}

func (h *PrepareWorkExecutionHandler) advance(ctx context.Context, request InitialExecutionRequest) (string, error) {
	if request.Rejected {
		return "", ErrInitialExecutionRejected
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return "", err
	}
	if err := request.AdmitRepositoryPreparation(session); err != nil {
		return "", err
	}
	if request.Submitted && request.Prepared {
		return request.WorkID, nil
	}
	if !request.Prepared {
		// The plan precedes the start's environment: its preparation process
		// checks the planned repositories out before the Harness starts.
		if err := request.PlanCheckouts(session); err != nil {
			return h.reject(ctx, request, errors.Join(ErrPreparationRejected, err))
		}
		request.Prepared = true
		request.RecoveryPending = false
		if err := h.requests.Save(ctx, request); err != nil {
			return "", err
		}
		request, err = h.requests.Get(ctx, request.WorkID)
		if err != nil {
			return "", err
		}
	}
	// Admission was committed when this Work was established: its execution
	// submits its creation once this preparation is recorded, never
	// rechecking capacity or reinterpreting a retry as Start.
	return request.WorkID, nil
}

func (h *PrepareWorkExecutionHandler) reject(ctx context.Context, request InitialExecutionRequest, cause error) (string, error) {
	// A created Work already owns execution admission. Preserve its first
	// input and mark the preparation outcome for normal execution-end recovery.
	request.Rejected = true
	request.StartFailed = true
	request.FailureReason = preparationFailureReason(cause)
	if err := h.requests.Save(ctx, request); err != nil {
		return "", errors.Join(cause, err)
	}
	return "", cause
}

func validateInitialPreparation(input PrepareWorkExecution) error {
	if err := input.Input.Validate(); err != nil {
		return err
	}
	if uuid.Validate(input.RequestID) != nil {
		return domain.ErrInvalidWork
	}
	return nil
}

func preparationFailureReason(cause error) string {
	switch {
	case errors.Is(cause, domain.ErrComputerFull):
		return "The Work's Computer already runs as many Works as its Capacity allows. Try again once one ends, or raise its Capacity."
	case errors.Is(cause, domain.ErrCapacityUnavailable):
		return "Execution capacity is unavailable. Try again later."
	case errors.Is(cause, domain.ErrComputerUnreachable):
		return "The Work's Computer is unreachable. Try again once it is online."
	case errors.Is(cause, domain.ErrComputerOutdated):
		return "The Work's Computer runs an outdated Talgent Computer. Upgrade it before retrying."
	case errors.Is(cause, domain.ErrComputerRemoved):
		return "The Work's Computer was removed."
	case errors.Is(cause, domain.ErrComputerNoSandbox):
		return "The Work's Computer cannot provide Claude Code's sandbox, which this Work runs in."
	case errors.Is(cause, domain.ErrComputerNotUsable):
		return "The Work's Computer cannot run this Work."
	case errors.Is(cause, ErrStartRequestCanceled):
		return "The startup request was cancelled."
	default:
		return "Repository preparation was rejected. Check the configuration before retrying."
	}
}

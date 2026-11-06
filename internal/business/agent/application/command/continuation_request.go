package command

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

var ErrContinuationConflict = errors.New("agent: another continuation request is retained")

// ContinuationRequest retains caller correlation across admission uncertainty.
// It is application recovery data, not a second Session lifecycle.
type ContinuationRequest struct {
	StartWork
	Operation string
	Rejection string
	Resolved  bool
}

type ContinuationRequests interface {
	RetainContinuation(context.Context, ContinuationRequest, int) (ContinuationRequest, error)
	GetContinuation(context.Context, string) (ContinuationRequest, bool, error)
	RecordContinuationRejection(context.Context, string, string, string) error
	ResolveContinuation(context.Context, string, string, bool) error
}

type workContinuation struct {
	startup  workStartup
	requests ContinuationRequests
}

func (h workContinuation) handle(ctx context.Context, input StartWork, operation string) error {
	// The observed version is compared once, against the Work read below.
	// Retaining the continuation advances that version, so the admission that
	// follows must not compare against it again.
	observed := input
	input.ExpectedVersion = nil
	if err := h.startup.authorization.AuthorizeWork(ctx, input.SubjectID, input.WorkID); err != nil {
		return err
	}
	if uuid.Validate(input.RequestID) != nil {
		return query.ErrInvalidStartRequest
	}
	// An earlier admitted request remains idempotent even after the current
	// continuation record has been replaced by a subsequent explicit request.
	if _, found, err := h.startup.admissions.FindAdmittedStart(ctx, input.RequestID); err != nil {
		return err
	} else if found {
		_, err := h.submit(ctx, ContinuationRequest{StartWork: input, Operation: operation})
		return err
	}
	session, err := h.startup.sessions.Get(ctx, input.WorkID)
	if err != nil {
		return err
	}
	if err := observed.admitObservedVersion(session); err != nil {
		return err
	}
	request, err := h.requests.RetainContinuation(ctx, ContinuationRequest{StartWork: input, Operation: operation}, session.Version)
	if err != nil {
		return err
	}
	if request.Rejection != "" {
		return h.settleRejection(ctx, request)
	}
	admitted, err := h.submit(ctx, request)
	if admitted {
		return errors.Join(err, h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, true))
	}
	if reason := continuationRejection(err); reason != "" {
		// Persist the rejection obligation before canceling late admission. If
		// either step loses its response, observation can finish this same work
		// without ever requesting new capacity.
		if saveErr := h.requests.RecordContinuationRejection(ctx, request.WorkID, request.RequestID, reason); saveErr != nil {
			if errors.Is(saveErr, ErrContinuationConflict) {
				// A competing retry may have admitted and resolved this exact
				// request after submit's negative lookup. A durable admission
				// wins; do not reject it or alter a newer continuation record.
				accepted, found, lookupErr := h.startup.admissions.FindAdmittedStart(ctx, request.RequestID)
				if lookupErr != nil {
					return lookupErr
				}
				if found && accepted.WorkID == request.WorkID && accepted.Kind == query.StartKindResume {
					return nil
				}
			}
			return saveErr
		}
		retained, found, readErr := h.requests.GetContinuation(ctx, request.WorkID)
		if readErr != nil {
			return readErr
		}
		if !found || retained.RequestID != request.RequestID {
			return ErrContinuationConflict
		}
		return h.settleRejection(ctx, retained)
	}
	return err
}

func (h workContinuation) submit(ctx context.Context, request ContinuationRequest) (bool, error) {
	switch request.Operation {
	case "resume":
		return h.startup.apply(ctx, request.StartWork, (*domain.AgentWork).Resume, fixedKind(query.StartKindResume), query.StartKindResume)
	default:
		return false, ErrContinuationConflict
	}
}

func (h workContinuation) settleRejection(ctx context.Context, request ContinuationRequest) error {
	canceled, err := h.startup.admissions.CancelStart(ctx, request.WorkID, request.RequestID)
	if err != nil {
		return err
	}
	if !canceled {
		// Admission won the shared serialization, including a start that
		// already completed before its response was observed.
		accepted, found, err := h.startup.admissions.FindAdmittedStart(ctx, request.RequestID)
		if err != nil {
			return err
		}
		if !found || accepted.WorkID != request.WorkID || accepted.Kind != query.StartKindResume {
			return errors.Join(ErrStartRequestConflict, h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, false))
		}
		return h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, true)
	}
	if err := h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, false); err != nil {
		return err
	}
	return continuationError(request.Rejection)
}

// recover observes an admitted start or finishes a previously recorded
// rejection. It never submits Resume or checks capacity.
func (h workContinuation) recover(ctx context.Context, sessionID string) error {
	request, found, err := h.requests.GetContinuation(ctx, sessionID)
	if err != nil || !found || request.Resolved {
		return err
	}
	if request.Rejection != "" {
		err := h.settleRejection(ctx, request)
		if continuationRejection(err) != "" {
			return nil
		}
		return err
	}
	accepted, found, err := h.startup.admissions.FindAdmittedStart(ctx, request.RequestID)
	if err != nil || !found {
		return err
	}
	if accepted.WorkID != request.WorkID || accepted.Kind != query.StartKindResume {
		if err := h.requests.RecordContinuationRejection(ctx, request.WorkID, request.RequestID, "conflict"); err != nil {
			return err
		}
		return h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, false)
	}
	return h.requests.ResolveContinuation(ctx, request.WorkID, request.RequestID, true)
}

func continuationRejection(err error) string {
	// A full Managed Computer is also unavailable capacity; its own reason
	// is matched first.
	if errors.Is(err, domain.ErrComputerFull) {
		return "computer_full"
	}
	for reason, target := range continuationErrors {
		if errors.Is(err, target) {
			return reason
		}
	}
	return ""
}

var continuationErrors = map[string]error{
	"reclamation_pending": domain.ErrComputeReclamationPending,
	"capacity":            domain.ErrCapacityUnavailable,
	"computer_not_usable": domain.ErrComputerNotUsable,
	"unreachable":         domain.ErrComputerUnreachable,
	"computer_removed":    domain.ErrComputerRemoved,
	"computer_outdated":   domain.ErrComputerOutdated,
	"computer_no_sandbox": domain.ErrComputerNoSandbox,
	"model_withdrawn":     domain.ErrModelConfigurationWithdrawn,
	"transition":          domain.ErrWorkTransition,
	"canceled":            ErrStartRequestCanceled,
	"not_permitted":       domain.ErrExecutionNotPermitted,
	"conflict":            ErrStartRequestConflict,
}

func continuationError(reason string) error {
	// Matched apart from the map, as continuationRejection records it.
	if reason == "computer_full" {
		return domain.ErrComputerFull
	}
	if err := continuationErrors[reason]; err != nil {
		return err
	}
	return ErrContinuationConflict
}

// RejectedAsUnreachable reports whether a recorded continuation rejection is
// its Computer's refusal as unreachable, the one refusal whose Resume is
// requested again on reconnection.
func RejectedAsUnreachable(reason string) bool {
	return reason != "" && errors.Is(continuationError(reason), domain.ErrComputerUnreachable)
}

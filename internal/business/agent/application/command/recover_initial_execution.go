package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type RecoverInitialExecutionHandler struct {
	requests    InitialExecutionRequests
	sessions    domain.AgentWorkRepository
	preparation *PrepareWorkExecutionHandler
}

func NewRecoverInitialExecutionHandler(requests InitialExecutionRequests, sessions domain.AgentWorkRepository, preparation *PrepareWorkExecutionHandler) *RecoverInitialExecutionHandler {
	return &RecoverInitialExecutionHandler{requests: requests, sessions: sessions, preparation: preparation}
}

func (h *RecoverInitialExecutionHandler) Handle(ctx context.Context, sessionID string) (result error) {
	defer func() { recordPreparationRecovery(ctx, h.requests, sessionID, result) }()
	request, err := h.requests.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	session, err := h.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if session.AuthorizePreparationRelease() == nil {
		return releaseInitialPreparation(ctx, h.requests, session, request)
	}
	if request.FirstStartRecovered() {
		return nil
	}
	if request.Rejected {
		return h.finishRejected(ctx, request)
	}
	// An incomplete first start waits while the Work holds no admitted start;
	// its next Resume or Restart completes it.
	if !session.HoldsActiveProcess() {
		return ErrInitialExecutionPending
	}
	if _, err := h.preparation.advance(ctx, request); err != nil {
		return err
	}
	if request, err = h.requests.Get(ctx, sessionID); err != nil {
		return err
	}
	if request.InputPrepared {
		return nil
	}
	// Checkout outcomes are reported by the preparation that owns them.
	// A ready Harness does not establish whether those outcomes committed.
	return ErrInitialExecutionPending
}

// A failed preparation is durable before its start is ended. Recovery applies
// it while the admitted first start still holds its process. The rejected
// input and plan stay for an explicit Restart; only permanent closure fences
// them, and nothing outside Agent holds resources for them.
func (h *RecoverInitialExecutionHandler) finishRejected(ctx context.Context, request InitialExecutionRequest) error {
	if request.CleanupConfirmed || !request.StartFailed || request.RejectionApplied {
		return nil
	}
	session, err := h.sessions.Get(ctx, request.WorkID)
	if err != nil {
		return err
	}
	latest, err := h.requests.Get(ctx, request.WorkID)
	if err != nil {
		return err
	}
	if latest.Version != request.Version {
		return domain.ErrConcurrentModification
	}
	if session.HoldsProcess() {
		if session.State != domain.WorkStopping {
			if err := session.RequestExecutionEnd(request.RequestID); err != nil {
				return err
			}
			if err := h.sessions.Save(ctx, session); err != nil {
				return err
			}
		}
	}
	latest.RejectionApplied = true
	return h.requests.Save(ctx, latest)
}

// Recovery diagnostics are display-only. A failed diagnostic write must not
// replace the original outcome or erase the retained recovery obligation.
func recordPreparationRecovery(ctx context.Context, requests InitialExecutionRequests, sessionID string, cause error) {
	request, err := requests.Get(ctx, sessionID)
	if err != nil || request.Rejected || request.FirstStartRecovered() {
		return
	}
	recovering := cause != nil && !errors.Is(cause, ErrInitialExecutionPending) && !errors.Is(cause, domain.ErrConcurrentModification)
	if request.RecoveryPending == recovering {
		return
	}
	request.RecoveryPending = recovering
	_ = requests.Save(ctx, request)
}

package command

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// CloseWorkHandler is the one way to close a Work permanently, for the Portal
// and for business callers alike. A running Work is stopped first; its close
// request applies once it has stopped, through ApplyKeptClose.
type CloseWorkHandler struct {
	control       sessionControl
	initial       InitialExecutionReader
	startup       query.WorkStartupReader
	continuations query.ContinuationReader
	buffer        RunningHarnessPort
}

func NewCloseWorkHandler(sessions domain.AgentWorkRepository, authorization access.WorkAuthorizer, execution domain.ExecutionPort, initial InitialExecutionReader, startup query.WorkStartupReader, continuations query.ContinuationReader, buffer RunningHarnessPort) *CloseWorkHandler {
	return &CloseWorkHandler{control: sessionControl{sessions: sessions, access: authorization, execution: execution}, initial: initial, startup: startup, continuations: continuations, buffer: buffer}
}

func (h *CloseWorkHandler) Handle(ctx context.Context, request WorkControl) error {
	return h.control.apply(ctx, request, func(work *domain.AgentWork, ctx context.Context, requestID string, port domain.ExecutionPort) error {
		facts, err := h.facts(ctx, work)
		if err != nil {
			return err
		}
		return work.Close(ctx, requestID, facts, port)
	})
}

// ApplyKeptClose applies the close request a Work kept while it stopped. The
// Work's recovery calls it, woken by WorkExecutionEnded or found by the sweep;
// it does nothing for a Work with no such request or still stopping. Recovery
// empties the input buffer of a Work that entered Closing.
func (h *CloseWorkHandler) ApplyKeptClose(ctx context.Context, workID string) error {
	work, err := h.control.sessions.Get(ctx, workID)
	if err != nil {
		return err
	}
	if !work.KeptCloseApplicable() {
		return nil
	}
	facts, err := h.facts(ctx, work)
	if err != nil {
		return err
	}
	requests := deferExecution(h.control.execution)
	if err := work.Close(ctx, work.CloseRequestReference, facts, requests); err != nil {
		return err
	}
	if err := h.control.sessions.Save(ctx, work); err != nil {
		return err
	}
	return requests.send(ctx)
}

// facts supplies the obligations a close must not discard.
func (h *CloseWorkHandler) facts(ctx context.Context, work *domain.AgentWork) (domain.ClosureFacts, error) {
	var facts domain.ClosureFacts
	if work.Closed() {
		return facts, nil
	}
	continuation, err := h.continuations.GetWorkContinuation(ctx, work.ID)
	if err != nil {
		return facts, err
	}
	facts.ContinuationPending = continuation.Pending
	startup, err := h.startup.GetWorkStartup(ctx, work.ID)
	if err != nil {
		return facts, err
	}
	facts.StartupPending = startup.CreationPending
	initial, err := h.initial.Get(ctx, work.ID)
	if err != nil && !errors.Is(err, ErrInitialExecutionNotFound) {
		return facts, err
	}
	if err == nil && !initial.InputBuffered && !(initial.Rejected && initial.CleanupConfirmed) {
		facts.StartupPending = true
	}
	buffered, err := h.buffer.ReadBuffer(ctx, work.ID)
	facts.InputPending = buffered.Pending()
	return facts, err
}

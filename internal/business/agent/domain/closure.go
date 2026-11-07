package domain

import (
	"context"
	"strings"
	"time"
)

// ClosureFacts are current application-owned obligations. Their observation
// and the Close commit share the input and execution admission order.
type ClosureFacts struct {
	// InputPending: accepted input still awaits processing. Only a running
	// Work's close waits for it; a stopped or failed Work's close drops it.
	InputPending bool
	// StartupPending: the first start still owes its initial task input.
	StartupPending bool
	// ContinuationPending: a Resume or Restart request is not yet settled.
	ContinuationPending bool
}

// Close permanently closes the Work. Closing applies to a stopped or failed
// Work; a running Work is first stopped normally, between Turns, and keeps the
// close request, which applies once the Work has stopped (WorkExecutionEnded).
// Repeating an accepted close confirms it without reopening admission.
func (s *AgentWork) Close(ctx context.Context, requestID string, facts ClosureFacts, port ExecutionPort) error {
	if strings.TrimSpace(requestID) == "" || len(requestID) > 128 {
		return ErrInvalidWork
	}
	if s.WaitsForDesk() {
		// The close withdraws the waiting start first, then applies to the
		// resulting Stopped or Failed Work; a first start is closed at once.
		if s.HarnessProcess.ContextMode != ContextFresh && facts.ContinuationPending {
			return ErrWorkTransition
		}
		s.withdrawWaitingStart(requestID)
	}
	if s.beyondReach() {
		return s.closeBeyondReach(ctx, requestID, facts, port)
	}
	switch s.State {
	case WorkClosing, WorkClosed:
		return nil
	case WorkStopping:
		// The close applies once the stop admitted earlier completes.
		if s.CloseRequestReference == "" {
			s.CloseRequestReference = requestID
		}
		return nil
	case WorkStopped, WorkFailed:
		if facts.ContinuationPending {
			return ErrWorkTransition
		}
		s.close(requestID)
		return nil
	case WorkWorking:
		if s.HarnessProcess == nil {
			return ErrInvalidWork
		}
		if s.HarnessProcess.Turn.Active {
			return ErrTurnActive
		}
		if facts.InputPending {
			return ErrInputPending
		}
		if facts.StartupPending || facts.ContinuationPending || s.EndRequestReference != "" {
			return ErrWorkTransition
		}
		s.CloseRequestReference = requestID
		return s.stop(ctx, requestID, StopReasonCompleted, port)
	default:
		return ErrWorkTransition
	}
}

// close enters Closing; the original close request identity is retained as
// acceptance metadata, independently of the execution stop request.
func (s *AgentWork) close(requestID string) {
	if s.CloseRequestReference == "" {
		s.CloseRequestReference = requestID
	}
	if s.HarnessProcess != nil {
		s.HarnessProcess.IdleSince, s.HarnessProcess.WaitingSince = time.Time{}, time.Time{}
	}
	s.State = WorkClosing
	s.record(WorkClosingEntered{WorkID: s.ID})
	s.confirmTermination()
	if s.State == WorkClosed {
		return
	}
	if s.StopRequestReference == "" {
		s.StopRequestReference = s.CloseRequestReference
	}
}

// Closed reports whether the Work entered Closing or Closed.
func (s *AgentWork) Closed() bool { return s.State == WorkClosing || s.State == WorkClosed }

// ClosureConfirmed reports whether the Work reached Closed: its workspace is
// then neither browsed nor read, whether or not it was reclaimed yet.
func (s *AgentWork) ClosureConfirmed() bool { return s.State == WorkClosed }

// KeptCloseApplicable reports a close request kept while the Work stopped
// that can apply now that it has stopped or failed.
func (s *AgentWork) KeptCloseApplicable() bool {
	return s.CloseRequestReference != "" && (s.State == WorkStopped || s.State == WorkFailed)
}

// endExecution records that the Work's execution ended, stopped or failed;
// a close request it kept now applies.
func (s *AgentWork) endExecution() {
	s.DisconnectedSince = time.Time{}
	s.record(WorkExecutionEnded{WorkID: s.ID})
}

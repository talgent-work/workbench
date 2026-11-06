package domain

import (
	"context"
	"time"
)

// startWithdrawnReason is a Restart's failure once its waiting start was
// withdrawn, the Work's earlier failure having been cleared by that start.
const startWithdrawnReason = "The restart was withdrawn while it waited for room on its Computer."

// WaitsForDesk reports a start that waits in its Computer's line: the Work
// stays Starting and its startup is deferred until its Desk is seated.
func (s *AgentWork) WaitsForDesk() bool {
	return s.State == WorkStarting && s.HarnessProcess != nil && s.HarnessProcess.DeskWaiting
}

// BeginSeatedStart applies the supplied fact that the waiting start's Desk is
// seated at now, from which its Startup Deadline counts, and requests its
// startup. The result reports a state change; a start not waiting, or already
// seated, is unchanged.
func (s *AgentWork) BeginSeatedStart(ctx context.Context, now time.Time, port ExecutionPort) (bool, error) {
	if !s.WaitsForDesk() {
		return false, nil
	}
	if now.IsZero() || port == nil {
		return false, ErrInvalidWork
	}
	p := s.HarnessProcess
	p.DeskWaiting, p.SeatedAt = false, now.UTC()
	return true, executionRequestError(s.startExecution(ctx, port))
}

// withdrawWaitingStart withdraws a start whose Desk still waits, on a stop or
// close request: a Resume returns to Stopped, a Restart to Failed, and a first
// start is closed at once. The withdrawn start launched nothing and holds no
// compute, so its Desk leaves and nothing is reclaimed.
func (s *AgentWork) withdrawWaitingStart(requestID string) {
	p := s.HarnessProcess
	p.DeskWaiting, p.StartWithdrawn, p.ComputeReclaimed = false, true, true
	s.releaseDesk()
	switch p.ContextMode {
	case ContextContinue:
		s.StopRequestReference, s.StopReason, s.State = requestID, StopReasonUserStopped, WorkStopped
		s.endExecution()
	case ContextRecover:
		s.recordFailure(startWithdrawnReason)
	default:
		s.close(requestID)
	}
}

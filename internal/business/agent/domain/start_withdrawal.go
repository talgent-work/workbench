package domain

// startWithdrawnReason is a Restart's failure once its waiting start was
// withdrawn, the Work's earlier failure having been cleared by that start.
const startWithdrawnReason = "The restart was withdrawn while it waited for room on its Computer."

// WaitsForDesk reports a start whose AgentExecution waits in its Computer's
// line, as the supplied fact startQueued says: the Work stays Starting until
// it is seated (Waiting start).
func (s *AgentWork) WaitsForDesk(startQueued bool) bool {
	return startQueued && s.State == WorkStarting && s.HarnessProcess != nil
}

// withdrawWaitingStart withdraws a start whose Desk still waits, on a stop or
// close request: a Resume returns to Stopped, a Restart to Failed, and a first
// start is closed at once (Withdrawn start). The withdrawn start launched
// nothing and holds no compute; its execution retires and leaves its Desk.
func (s *AgentWork) withdrawWaitingStart(requestID string) {
	p := s.HarnessProcess
	p.DeskWaiting, p.StartWithdrawn, p.ComputeReclaimed = false, true, true
	s.requestComputeRelease()
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

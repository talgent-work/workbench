package domain

import "errors"

// Retry on reconnection: input that awaits
// a Work's Managed Computer while the machine is away is taken up once more
// when it reconnects. A Resume refused because the machine was unreachable,
// or a Working Work stopped beyond reach with input awaiting processing,
// marks the Stopped Work ReachResumeDue; its Computer's reconnection then
// requests the Resume once, so input that arrived meanwhile is read without
// anyone asking again. Every admitted start clears the mark, and so do
// ReachResumeAttempts retries that failed without an answer.

// RefuseResume applies its Computer's refusal of a Resume: a Stopped Work
// refused because its Computer is unreachable records ReachResumeDue and
// stays Stopped, and a repeated unreachable refusal keeps it. A refusal for
// any other reason spends a mark the Work holds: the Resume it owed was
// requested and answered. The caller supplies refusals only, never a failure
// to ask. It reports whether the Work changed.
func (s *AgentWork) RefuseResume(refusal error) bool {
	if errors.Is(refusal, ErrComputerUnreachable) {
		if s.State != WorkStopped || s.ReachResumeDue {
			return false
		}
		s.ReachResumeDue = true
		return true
	}
	if refusal == nil || !s.ReachResumeDue {
		return false
	}
	s.spendReachResume()
	return true
}

// ResumeOnReconnection applies its Computer's reconnection to the mark: a
// marked Stopped Work holding input awaiting processing, as the supplied fact
// of its input buffer says, has its Resume requested once more (resume). A
// marked Work without such input, or no longer Stopped, spends the mark
// instead (changed).
func (s *AgentWork) ResumeOnReconnection(inputPending bool) (resume, changed bool) {
	if !s.ReachResumeDue {
		return false, false
	}
	if s.State == WorkStopped && inputPending {
		return true, false
	}
	s.spendReachResume()
	return false, true
}

// ReachResumeAttempts bounds the retries of an owed Resume that fail without
// an answer, such as a Work the platform cannot read or an admission that
// errs. The Work's recovery retries the Resume on every sweep while its
// Computer is reachable; the failure reaching the ceiling spends the mark, and
// the input stays buffered for the Work's next Resume.
const ReachResumeAttempts = 30

// RecordResumeRetryFailure records a retry of its owed Resume that failed
// without an answer. It reports whether this failure reached
// ReachResumeAttempts and spent the mark; any other failure changes the Work
// only while it is marked.
func (s *AgentWork) RecordResumeRetryFailure() (spent bool) {
	if !s.ReachResumeDue {
		return false
	}
	s.ReachResumeFailures++
	if s.ReachResumeFailures < ReachResumeAttempts {
		return false
	}
	s.spendReachResume()
	return true
}

// spendReachResume clears the mark together with its failed retries.
func (s *AgentWork) spendReachResume() {
	s.ReachResumeDue, s.ReachResumeFailures = false, 0
}

// awaitReconnection marks a Managed Work just stopped beyond reach while
// input awaited processing: that input waits for its machine's return.
func (s *AgentWork) awaitReconnection(inputPending bool) {
	if inputPending && s.State == WorkStopped && s.OnManagedComputer() {
		s.ReachResumeDue = true
	}
}

package domain

import "errors"

// Retry on reconnection: input that awaits
// a Work's Managed Computer while the machine is away is taken up once more
// when it reconnects. A Resume refused because the machine was unreachable,
// or a Working Work stopped beyond reach with input awaiting processing,
// marks the Stopped Work ReachResumeDue; its Computer's reconnection then
// requests the Resume once, so input that arrived meanwhile is read without
// anyone asking again. Every admitted start clears the mark.

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
	s.ReachResumeDue = false
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
	s.ReachResumeDue = false
	return false, true
}

// awaitReconnection marks a Managed Work just stopped beyond reach while
// input awaited processing: that input waits for its machine's return.
func (s *AgentWork) awaitReconnection(inputPending bool) {
	if inputPending && s.State == WorkStopped && s.OnManagedComputer() {
		s.ReachResumeDue = true
	}
}

package domain

import (
	"time"

	"github.com/google/uuid"
)

// ObserveTurn replaces root-Turn feedback and consumes current admission facts.
// Stream positions of the current process deduplicate replays monotonically; a
// new process starts its own positions, so they cannot fence earlier processes.
// A running Turn is not idle, and is Waiting while a required interaction is
// unanswered. A new Turn ends the Waiting a stopped process left behind, since
// the Turn is no longer paused; a question it asks again starts Waiting anew.
// An ended Turn, whatever its outcome, ends Waiting and leaves the Work idle
// unless accepted input awaits processing.
func (s *AgentWork) ObserveTurn(fact TurnObservation, inputPending, requiredResponse bool, now time.Time) error {
	if fact.Sequence <= 0 || (fact.Active && fact.Completed) || now.IsZero() {
		return ErrInvalidWork
	}
	if !s.processing() {
		return nil
	}
	// A position not after the last applied one repeats a replayed stream.
	if fact.Sequence <= s.HarnessProcess.Turn.Sequence {
		return nil
	}
	started := fact.Active && !s.HarnessProcess.Turn.Active
	s.HarnessProcess.Turn = fact
	s.HarnessProcess.PhaseSequence = fact.Sequence
	if fact.Active {
		s.HarnessProcess.IdleSince = time.Time{}
		if started {
			s.HarnessProcess.WaitingSince = time.Time{}
		}
		s.applyWaiting(requiredResponse, now)
		return nil
	}
	s.HarnessProcess.WaitingSince = time.Time{}
	return s.RecordIdleness(inputPending, now)
}

// RecordIdleness establishes IdleSince for a Working Work with no active Turn
// and no accepted input awaiting processing, even while it keeps the Waiting a
// stopped process left behind: no Turn is paused. Application supplies the
// accepted-input fact when input settles, when a resolved interaction leaves
// no required response, when readiness is confirmed and when recovery
// reconciles the Work; a repeated fact keeps the original origin.
func (s *AgentWork) RecordIdleness(inputPending bool, now time.Time) error {
	if now.IsZero() {
		return ErrInvalidWork
	}
	if !s.processing() || inputPending || s.HarnessProcess.Turn.Active {
		return nil
	}
	if s.HarnessProcess.IdleSince.IsZero() {
		s.HarnessProcess.IdleSince = now.UTC()
	}
	return nil
}

// Idle reports a Working Work with no active Turn and nothing to process.
func (s *AgentWork) Idle() bool {
	return s.processing() && !s.HarnessProcess.IdleSince.IsZero()
}

// ObserveTurnActivity ends idleness on the executor's report that the Work's
// Turn progresses, which arrives at most once a minute while it runs. It
// reports whether the Work changed.
func (s *AgentWork) ObserveTurnActivity() bool {
	if !s.Idle() {
		return false
	}
	s.HarnessProcess.IdleSince = time.Time{}
	return true
}

// Waiting reports a Work whose user owes the answer to a necessary
// authorization or question. It survives a stop, a failure and a new start,
// so a forgotten question stays visible, and ends with an answer, a new Turn
// or closure.
func (s *AgentWork) Waiting() bool {
	return s.HarnessProcess != nil && !s.HarnessProcess.WaitingSince.IsZero()
}

// heldByQuestion reports a running Turn paused by an unanswered question.
func (s *AgentWork) heldByQuestion() bool {
	return s.processing() && s.HarnessProcess.Turn.Active && s.Waiting()
}

// ApplyResponseRequirement consumes current interaction facts supplied together
// with platform acceptance evidence. It retains no request collection or flag.
func (s *AgentWork) ApplyResponseRequirement(sequence int64, required bool, now time.Time) error {
	if sequence <= 0 || now.IsZero() {
		return ErrInvalidWork
	}
	// Repeating the same native observation cannot reverse a later platform
	// acceptance, which has already committed the authoritative Work state.
	if !s.processing() || sequence <= s.HarnessProcess.PhaseSequence {
		return nil
	}
	s.HarnessProcess.PhaseSequence = sequence
	// No required interaction in a process with no running Turn says nothing
	// about the question an earlier process left unanswered.
	if required || s.HarnessProcess.Turn.Active {
		s.applyWaiting(required, now)
	}
	return nil
}

// applyWaiting starts Waiting when a required interaction is unanswered,
// keeping the origin of an ongoing wait, and ends it when none remains.
func (s *AgentWork) applyWaiting(required bool, now time.Time) {
	if !required {
		s.HarnessProcess.WaitingSince = time.Time{}
		return
	}
	s.HarnessProcess.IdleSince = time.Time{}
	if s.HarnessProcess.WaitingSince.IsZero() {
		s.HarnessProcess.WaitingSince = now.UTC()
	}
}

// AcceptInput admits ordinary input in every state but Closing and Closed;
// a running Turn or a pending Required Response does not reject it. Accepting
// ends the Work's idleness and keeps its state: the buffered input waits for
// the Work's current or next Harness, and accepting never starts the Work. A
// Waiting Turn stays Waiting, since the Harness holds the input behind the
// unanswered interaction. A replay of an already buffered or settled identity
// creates no new work and ends no newer idle interval (Input identity).
func (s *AgentWork) AcceptInput(replay bool) error {
	return s.accept(replay, false)
}

// AcceptResponse accepts a response under the same condition as ordinary
// input and ends Waiting. Whether its interaction is still open is judged at
// delivery; while another required interaction remains, the next interaction
// feedback restores Waiting (Apply response requirement).
func (s *AgentWork) AcceptResponse(replay bool) error {
	return s.accept(replay, true)
}

func (s *AgentWork) accept(replay, answers bool) error {
	if err := s.AdmitInput(); err != nil {
		return err
	}
	if s.InitialTaskInput != nil {
		return ErrInitialTaskInputPending
	}
	// Input for a Work that is not running, or whose process end is already
	// requested, waits in the buffer for the next start; its task begins when
	// a Harness reads it.
	if replay || !s.processing() || s.EndRequestReference != "" {
		return nil
	}
	s.HarnessProcess.IdleSince = time.Time{}
	if answers {
		s.HarnessProcess.WaitingSince = time.Time{}
	}
	return nil
}

// AdmitInput admits input and interaction replies under the Work's lifecycle:
// every state but Closing and Closed holds them in its input buffer.
func (s *AgentWork) AdmitInput() error {
	if s.Closed() {
		return ErrWorkTransition
	}
	return nil
}

// processing reports a Work whose Harness process is ready (Working).
func (s *AgentWork) processing() bool { return s.State == WorkWorking }

// BufferedWork is what the Work's input buffer still holds for its process.
// Any buffered reply lets the Harness continue: it answers a pending
// interaction, so it defers the timeout whichever request it answers.
type BufferedWork struct {
	Input, Reply bool
}

// WaitingTimeout is the universal continuous idle or Waiting fallback.
const WaitingTimeout = 30 * time.Minute

// timeoutOrigin is the origin of the interval the timeout measures: the
// paused Turn's Waiting, otherwise the idle Work's; zero when neither holds.
// Waiting a stopped process left behind pauses no Turn and is not measured.
func (s *AgentWork) timeoutOrigin() time.Time {
	if !s.processing() {
		return time.Time{}
	}
	if s.heldByQuestion() {
		return s.HarnessProcess.WaitingSince
	}
	return s.HarnessProcess.IdleSince
}

// TimeoutDue reports whether the Work has been idle or Waiting continuously
// for the timeout at now, before buffered input or replies are weighed.
func (s *AgentWork) TimeoutDue(now time.Time) bool {
	origin := s.timeoutOrigin()
	return !origin.IsZero() && now.Sub(origin) >= WaitingTimeout
}

// StopAfterTimeout applies the universal 30-minute rule to an idle or Waiting
// Work. Buffered input defers an idle timeout; a buffered reply defers either,
// since it answers the request that holds the Turn. A Turn held open by a
// Required Response never ends by itself, so the timeout cancels it and the
// stop follows once the Turn has ended (Stop between Turns). A progressing
// Turn is neither idle nor Waiting and never times out.
func (s *AgentWork) StopAfterTimeout(now time.Time, buffered BufferedWork) (bool, error) {
	if !s.TimeoutDue(now) || buffered.Reply || (buffered.Input && !s.heldByQuestion()) {
		return false, nil
	}
	// A Turn held open by a Required Response is cancelled first, then the
	// process asked to exit (Timeout cancels first).
	return true, s.stop(uuid.Must(uuid.NewV7()).String(), StopReasonWaitingTimeout)
}

// RequireReadyProcess admits a control that acts on the Work's running
// Harness process, such as cancelling its Turn or adjusting its thinking
// level: the Work must be Working and not being ended.
func (s *AgentWork) RequireReadyProcess() error {
	if !s.processing() || s.EndRequestReference != "" {
		return ErrWorkTransition
	}
	if s.HarnessProcess == nil {
		return ErrInvalidWork
	}
	return nil
}

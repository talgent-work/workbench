package domain

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrExecutionUnobserved answers a read or control that needs the Work's
// live execution environment while the platform does not observe it: during
// a Disconnection, or while Dispatcher reattaches its stream. It is an
// expected, retryable condition, not a failure.
var ErrExecutionUnobserved = errors.New("agent: the Work's execution environment is not observed")

// DisconnectionTimeout is how long a Working or Stopping Work may stay
// disconnected before it is stopped beyond reach (Disconnection timeout). A
// Managed Computer fences its Work processes before it passes, so the stop is
// true without a release confirmation; a Cloud Work's stop reclaims its sandbox
// instead.
const DisconnectionTimeout = 5 * time.Minute

// OnManagedComputer reports whether the Work runs on a Managed Computer:
// only such a Work has a Harness permission mode, fixed from its first start
// (Managed permission mode).
func (s *AgentWork) OnManagedComputer() bool { return s.PermissionMode != "" }

// Disconnected reports whether the platform has lost observation of the
// Work's execution environment.
func (s *AgentWork) Disconnected() bool { return !s.DisconnectedSince.IsZero() }

// ObservesEnvironment reports whether the Work has a run whose process may
// still be in its execution environment, so a Disconnection applies to it:
// a seated start, a Working or Stopping Work, or a Closing one whose compute
// is not released yet.
func (s *AgentWork) ObservesEnvironment() bool {
	p := s.HarnessProcess
	if p == nil || p.ComputeReclaimed {
		return false
	}
	switch s.State {
	case WorkStarting:
		return !p.DeskWaiting
	case WorkWorking, WorkStopping, WorkClosing:
		return true
	default:
		return false
	}
}

// countsDisconnection reports whether a loss of observation now is the
// Work's Disconnection. A Cloud Work's start is not yet observed through
// Dispatcher's attachment, which the start itself establishes: its Startup
// Deadline governs it, so its Disconnection counts only once it works.
func (s *AgentWork) countsDisconnection() bool {
	if !s.ObservesEnvironment() {
		return false
	}
	return s.OnManagedComputer() || s.State != WorkStarting
}

// disconnectionOrigin is the origin a loss at since gives the current run's
// Disconnection: never before its start, kept in UTC.
func (s *AgentWork) disconnectionOrigin(since time.Time) time.Time {
	if started := s.HarnessProcess.StartingSince; since.Before(started) {
		since = started
	}
	return since.UTC()
}

// sameOrLater compares origins as they are kept, to the millisecond, so a
// repeated fact compares equal.
func sameOrLater(origin, than time.Time) bool {
	return !origin.Truncate(time.Millisecond).Before(than.Truncate(time.Millisecond))
}

// RecordDisconnection sets the origin of the Work's Disconnection from the
// supplied observation fact that its environment was lost at since: its
// Managed Computer's Presence, or Dispatcher's attachment to its Cloud
// sandbox. A repeated or earlier fact keeps the origin of the Disconnection
// under way, while a later one is a new loss after a return of observation
// this Work never saw: a machine's fence counts from it, so the new
// Disconnection starts there. A Cloud Work's start records none. It changes
// no State and reports whether anything changed.
func (s *AgentWork) RecordDisconnection(since time.Time) (bool, error) {
	if since.IsZero() {
		return false, ErrInvalidWork
	}
	if !s.countsDisconnection() {
		return false, nil
	}
	since = s.disconnectionOrigin(since)
	if s.Disconnected() && sameOrLater(s.DisconnectedSince, since) {
		return false, nil
	}
	s.DisconnectedSince = since
	return true, nil
}

// EndDisconnection ends the Work's Disconnection once observation of its
// environment returns, and reports whether one was under way. A run
// reclaimed beyond reach keeps it until its release confirmation, which
// alone ends that run (Disconnection release).
func (s *AgentWork) EndDisconnection() bool {
	if !s.Disconnected() || (s.HarnessProcess != nil && s.HarnessProcess.Unreached) {
		return false
	}
	s.DisconnectedSince = time.Time{}
	return true
}

// EndDisconnectionOf ends the Work's Disconnection on a supplied return of
// observation after the loss at since. A return ends the Disconnection it
// follows, one starting no later than that loss, never a later one it
// precedes. It reports whether one ended.
func (s *AgentWork) EndDisconnectionOf(since time.Time) bool {
	if !s.Disconnected() || since.IsZero() || s.HarnessProcess == nil {
		return false
	}
	if !sameOrLater(s.disconnectionOrigin(since), s.DisconnectedSince) {
		return false
	}
	return s.EndDisconnection()
}

// DisconnectionDue reports whether the Work's Disconnection has lasted the
// Disconnection timeout at now while it is Working, Stopping or Closing.
func (s *AgentWork) DisconnectionDue(now time.Time) bool {
	if !s.Disconnected() || !s.ObservesEnvironment() || s.State == WorkStarting {
		return false
	}
	return !now.Before(s.DisconnectedSince.Add(DisconnectionTimeout))
}

// StopBeyondReach stops a Work whose Disconnection has lasted the
// Disconnection timeout at now, whatever its Turn facts; a Working Work's
// stop takes the disconnection reason, an admitted stop keeps its own. On a
// Managed Computer its machine fenced the process, which vouches for exit and
// release, so a Working or Stopping Work becomes Stopped and a Closing one
// Closed at once. On a Cloud its sandbox is reclaimed: a Working Work becomes
// Stopping, and its stop or close completes on the release confirmation.
// inputPending is the supplied fact that accepted input awaits processing: a
// Managed Work it stops so records ReachResumeDue, since that input awaits
// its machine's return (Retry on reconnection). The result reports a change.
func (s *AgentWork) StopBeyondReach(ctx context.Context, now time.Time, inputPending bool, port ExecutionPort) (bool, error) {
	if !s.DisconnectionDue(now) || s.HarnessProcess.Unreached {
		return false, nil
	}
	if port == nil {
		return false, ErrInvalidWork
	}
	working := s.State == WorkWorking
	if working {
		s.admitStop(uuid.Must(uuid.NewV7()).String(), StopReasonDisconnected)
	}
	err := s.endBeyondReach(ctx, port)
	if working {
		s.awaitReconnection(inputPending)
	}
	return true, err
}

// beyondReach reports a Work whose running environment the platform does not
// observe now: a stop or close requested now is admitted at once, whatever
// its Turn facts, and ends the run without reaching its process.
func (s *AgentWork) beyondReach() bool {
	return s.Disconnected() && s.ObservesEnvironment() && s.State != WorkStarting
}

// stopBeyondReach stops a Work during a Disconnection at once: a Working
// Work under requestID and reason, a Stopping one under the stop it
// admitted. It reports whether it applied.
func (s *AgentWork) stopBeyondReach(ctx context.Context, requestID string, reason StopReason, port ExecutionPort) (bool, error) {
	if !s.beyondReach() || s.State == WorkClosing {
		return false, nil
	}
	if strings.TrimSpace(requestID) == "" || port == nil {
		return false, ErrInvalidWork
	}
	if s.State == WorkWorking {
		s.admitStop(requestID, reason)
	}
	return true, s.endBeyondReach(ctx, port)
}

// closeBeyondReach closes a Work during a Disconnection at once: a Working
// Work is stopped as completed, as any close of a running Work is, and its
// run ends without reaching its process. Input still awaiting processing, or
// a start or continuation still owed, refuses it as on any running Work.
func (s *AgentWork) closeBeyondReach(ctx context.Context, requestID string, facts ClosureFacts, port ExecutionPort) error {
	if port == nil {
		return ErrInvalidWork
	}
	if s.State == WorkWorking {
		if facts.InputPending {
			return ErrInputPending
		}
		if facts.StartupPending || facts.ContinuationPending || s.EndRequestReference != "" {
			return ErrWorkTransition
		}
		s.admitStop(requestID, StopReasonCompleted)
	}
	if s.CloseRequestReference == "" {
		s.CloseRequestReference = requestID
	}
	err := s.endBeyondReach(ctx, port)
	if s.State == WorkStopped {
		s.close(requestID)
	}
	return err
}

// endBeyondReach ends a run the platform no longer observes the way its
// Computer allows: a Managed Computer's fence ended it already, while a
// Cloud sandbox is reclaimed in its place.
func (s *AgentWork) endBeyondReach(ctx context.Context, port ExecutionPort) error {
	if s.OnManagedComputer() {
		return s.endFenced(ctx, port)
	}
	return s.reclaimUnreached(ctx, port)
}

// endFenced ends a Managed Work's run as its machine's fence did: the process
// exited and its compute is released, so its Desk leaves, a Stopping Work is
// Stopped and a Closing one Closed. The Disconnection ends with the run. The
// execution port retires what the platform keeps for the run without
// reclaiming compute; the machine's reconciliation cleans up what remains
// when it reconnects.
func (s *AgentWork) endFenced(ctx context.Context, port ExecutionPort) error {
	p := s.HarnessProcess
	p.Exited, p.ComputeReclaimed, p.SubAgents = true, true, nil
	p.rest()
	s.DisconnectedSince = time.Time{}
	s.releaseDesk()
	s.confirmTermination()
	return executionRequestError(port.RetireFencedRun(ctx, s.ID))
}

// reclaimUnreached reclaims a Cloud Work's sandbox in place of the exit its
// process can no longer report, which stands in for a Managed Computer's
// fence: the release confirmation vouches for the exit, so the Work's stop
// or close completes on it, as any stop does (Disconnection release). The
// Disconnection stays shown until then.
func (s *AgentWork) reclaimUnreached(ctx context.Context, port ExecutionPort) error {
	p := s.HarnessProcess
	p.Unreached = true
	p.rest()
	return executionRequestError(port.ReclaimCompute(ctx, s.ID))
}

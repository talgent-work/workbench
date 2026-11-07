package domain

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestExecutionEnd retains a normal stop obligation for the admitted start.
// Startup must still establish readiness and a running Turn must end before
// Stop is admitted; ending a batch never permanently closes its conversation.
func (s *AgentWork) RequestExecutionEnd(requestID string) error {
	if s.HarnessProcess == nil || strings.TrimSpace(requestID) == "" {
		return ErrInvalidWork
	}
	switch s.State {
	case WorkStarting:
		if s.EndRequestReference == "" {
			s.EndRequestReference = requestID
		}
		return nil
	case WorkWorking:
		if s.EndRequestReference != "" {
			requestID = s.EndRequestReference
		}
		if s.HarnessProcess.Turn.Active {
			// Stop between Turns: the end waits for the running Turn.
			s.EndRequestReference = requestID
			return nil
		}
		return s.stop(requestID, StopReasonCompleted)
	case WorkFailed:
		if s.HarnessProcess.ended() {
			return nil
		}
		if s.EndRequestReference == "" {
			s.EndRequestReference = requestID
		}
		if s.StopRequestReference == "" {
			s.StopRequestReference = s.EndRequestReference
			s.requestStop()
		}
		return nil
	case WorkStopping, WorkStopped, WorkClosing, WorkClosed:
		// Existing completion/reclamation obligations remain with Agent. A second
		// business completion request does not replace the admitted control identity.
		return nil
	default:
		return ErrWorkTransition
	}
}

// Stop admits a user or caller stop only between Turns (Stop between Turns);
// repeating an admitted stop confirms it. startQueued is the supplied fact
// that the start's AgentExecution still waits for its Desk: the stop then
// withdraws the start (Withdrawn start).
func (s *AgentWork) Stop(requestID string, startQueued bool) error {
	if s.WaitsForDesk(startQueued) {
		if strings.TrimSpace(requestID) == "" {
			return ErrInvalidWork
		}
		s.withdrawWaitingStart(requestID)
		return nil
	}
	if stopped, err := s.stopBeyondReach(requestID, StopReasonUserStopped); stopped || err != nil {
		return err
	}
	if s.processing() && s.HarnessProcess != nil && s.HarnessProcess.Turn.Active {
		return ErrTurnActive
	}
	return s.stop(requestID, StopReasonUserStopped)
}

func (s *AgentWork) stop(requestID string, reason StopReason) error {
	if !s.processing() && !(s.State == WorkStopping && requestID == s.StopRequestReference) {
		return ErrWorkTransition
	}
	if strings.TrimSpace(requestID) == "" {
		return ErrInvalidWork
	}
	s.admitStop(requestID, reason)
	s.requestStop()
	return nil
}

// admitStop enters Stopping under requestID. A repeated stop keeps the
// original reason.
func (s *AgentWork) admitStop(requestID string, reason StopReason) {
	if s.processing() {
		s.HarnessProcess.rest()
		s.StopReason = reason
	}
	s.StopRequestReference, s.State = requestID, WorkStopping
}

// ApplyExecutionReport applies its AgentExecution's report about the current
// start (Apply execution report): readiness confirms it (Confirm readiness),
// a missed Startup Deadline fails it (Fail at Startup Deadline), an exit,
// rejection, abandonment or environment loss ends it (Reconcile process), and
// a release confirms its compute release (Confirm stop). A report about
// another start changes nothing. It reports whether the Work changed.
func (s *AgentWork) ApplyExecutionReport(report Event) (bool, error) {
	p := s.HarnessProcess
	if p == nil || !servesStart(reportedStart(report), p.StartingSince) {
		return false, nil
	}
	before := s.snapshot()
	var err error
	switch r := report.(type) {
	case ExecutionReady:
		s.confirmReadiness()
	case ExecutionStartFailed:
		err = s.failAtStartupDeadline(r.Reason)
	case ExecutionStartRejected:
		err = s.confirmStartRejected()
	case ExecutionStartAbandoned:
		s.confirmStartAbandoned()
	case ExecutionEnvironmentLost:
		s.confirmEnvironmentLost(r.Reason)
	case ExecutionExited:
		s.recordExit(r)
	case ExecutionReleasedEvent:
		s.confirmComputeReclaimed()
	default:
		return false, ErrInvalidWork
	}
	return !before.equal(s), err
}

// reportedStart is the admission time of the start a report concerns.
func reportedStart(report Event) (start time.Time) {
	switch r := report.(type) {
	case ExecutionReady:
		return r.StartAdmittedAt
	case ExecutionStartFailed:
		return r.StartAdmittedAt
	case ExecutionStartRejected:
		return r.StartAdmittedAt
	case ExecutionStartAbandoned:
		return r.StartAdmittedAt
	case ExecutionEnvironmentLost:
		return r.StartAdmittedAt
	case ExecutionExited:
		return r.StartAdmittedAt
	case ExecutionReleasedEvent:
		return r.StartAdmittedAt
	}
	return start
}

// confirmReadiness applies the ready report of a start still Starting,
// transitioning to Working (Confirm readiness); a start that already failed
// or ended stays as it is (No late Working).
func (s *AgentWork) confirmReadiness() {
	p := s.HarnessProcess
	if s.State != WorkStarting || p.ended() || p.EnvironmentLost {
		return
	}
	p.Ready = true
	s.State = WorkWorking
}

// failAtStartupDeadline records the failure its AgentExecution reports for a
// missed Startup Deadline, naming the earliest incomplete Required Startup
// Step, and asks the process it may hold to end (Fail at Startup Deadline).
func (s *AgentWork) failAtStartupDeadline(reason string) error {
	if s.State != WorkStarting {
		return nil
	}
	if strings.TrimSpace(reason) == "" {
		return ErrInvalidWork
	}
	s.recordFailure(reason)
	return s.RequestExecutionEnd(uuid.Must(uuid.NewV7()).String())
}

// recordExit applies the acknowledged exit of the current start's process
// under the Lifecycle observation rule: an exit after readiness while a stop
// is admitted is normal, whatever its code; every other exit is a failure
// that keeps its code and reason. Either way compute release follows.
func (s *AgentWork) recordExit(exited ExecutionExited) {
	p := s.HarnessProcess
	if s.State == WorkClosed || p.ended() || p.EnvironmentLost {
		return
	}
	if !exited.Ready || s.StopRequestReference == "" {
		// A failure already recorded for this start keeps explaining it; the
		// exit code is retained either way.
		reason := s.FailureReason
		if reason == "" {
			reason = strings.TrimSpace(exited.Exit.Reason)
		}
		if reason == "" {
			reason = "native_process_exited_unexpectedly"
		}
		s.recordFailure(fmt.Sprintf("%s (exit code %d)", reason, exited.Exit.ExitCode))
	}
	p.Exited = true
	p.SubAgents = nil
	p.rest()
	if s.State == WorkStarting || s.processing() {
		s.State = WorkStopping
		s.StopReason = StopReasonCompleted
	}
	s.requestComputeRelease()
}

// confirmComputeReclaimed applies the release confirmation of the current
// start's execution. Release alone cannot prove exit, environment loss,
// rejection or abandonment; only the release of a run reclaimed beyond reach
// vouches for its exit (Disconnection release).
func (s *AgentWork) confirmComputeReclaimed() {
	p := s.HarnessProcess
	if !(p.ended() || p.EnvironmentLost || p.Unreached) || p.ComputeReclaimed {
		return
	}
	if s.State != WorkStopping && s.State != WorkStopped && s.State != WorkFailed && s.State != WorkClosing && s.State != WorkClosed {
		return
	}
	p.ComputeReclaimed = true
	if p.Unreached && !p.Exited {
		// The released sandbox took with it the process the Work could no
		// longer reach (Disconnection release).
		p.Exited, p.SubAgents = true, nil
	}
	s.confirmTermination()
}

func (s *AgentWork) confirmTermination() {
	p := s.HarnessProcess
	if s.State == WorkClosing && (p == nil || (p.ComputeReclaimed && (p.ended() || p.EnvironmentLost))) {
		s.State = WorkClosed
		s.record(WorkClosedEvent{WorkID: s.ID, WorkspaceReference: s.WorkspaceReference})
	} else if s.State == WorkStopping && p.Exited && p.ComputeReclaimed {
		s.State = WorkStopped
		s.endExecution()
	}
}

// confirmEnvironmentLost applies the reported permanent loss of the execution
// environment. It records failure, never an exit, and asks for the
// execution's retirement; an already recorded exit keeps its normal
// completion semantics. A failure already recorded keeps explaining it;
// otherwise lossReason does, when the authority knows why.
func (s *AgentWork) confirmEnvironmentLost(lossReason string) {
	p := s.HarnessProcess
	if p.EnvironmentLost || p.ComputeReclaimed {
		return
	}
	if p.Exited || p.ended() || s.State == WorkStopped || s.State == WorkClosed {
		return
	}
	reason := s.FailureReason
	if reason == "" {
		reason = strings.TrimSpace(lossReason)
	}
	if reason == "" {
		reason = "execution environment lost"
	}
	s.recordFailure(reason)
	p.EnvironmentLost = true
	p.SubAgents = nil
	p.rest()
	s.requestComputeRelease()
}

// RecordFailure records an application-established inability to start the Harness.
// Input preparation or business task rejection does not establish process failure.
func (s *AgentWork) RecordFailure(reason string) error {
	if s.HarnessProcess == nil || s.State == WorkStopped || s.State == WorkClosed {
		return ErrWorkTransition
	}
	if strings.TrimSpace(reason) == "" {
		return ErrInvalidWork
	}
	s.recordFailure(reason)
	return nil
}

func (s *AgentWork) recordFailure(reason string) {
	s.HarnessProcess.rest()
	s.FailureReason = reason
	if s.State != WorkClosing && s.State != WorkStopped && s.State != WorkClosed && s.State != WorkFailed {
		s.State = WorkFailed
		s.endExecution()
	}
}

// HoldsProcess reports whether the Work counts toward its definition's
// concurrency limit.
func (s *AgentWork) HoldsProcess() bool {
	return s.HarnessProcess.Held()
}

// HoldsActiveProcess reports whether the Work holds the Harness process of an
// admitted start that has not ended: Starting or Working.
func (s *AgentWork) HoldsActiveProcess() bool {
	return s.HoldsProcess() && (s.State == WorkStarting || s.processing())
}

// confirmStartRejected applies the report that the start's environment could
// not be created: the Work becomes Failed without a stop or an exit, naming
// its execution environment, and its execution retires (Rejected start).
func (s *AgentWork) confirmStartRejected() error {
	p := s.HarnessProcess
	if p.StartRejected || p.ended() || p.EnvironmentLost {
		return nil
	}
	if p.Ready {
		return ErrInvalidWork
	}
	p.StartRejected = true
	s.recordFailure(startRejectedReason)
	s.requestComputeRelease()
	return nil
}

// confirmStartAbandoned applies the report that the start never launched a
// Harness process and no longer can. Nothing is left to stop, so its
// execution retires at once under the Abandoned start rule. Only a failed or
// closing Work's start is abandoned; one that became ready ends by its exit.
func (s *AgentWork) confirmStartAbandoned() {
	p := s.HarnessProcess
	if p.ended() || p.EnvironmentLost || p.Ready || (s.State != WorkFailed && s.State != WorkClosing) {
		return
	}
	p.StartAbandoned = true
	s.requestComputeRelease()
}

// workSnapshot is what applying a report can change, compared to tell
// whether it changed the Work.
type workSnapshot struct {
	state              WorkState
	failure, stop, end string
	reason             StopReason
	process            HarnessProcess
	events             int
}

func (s *AgentWork) snapshot() workSnapshot {
	snapshot := workSnapshot{state: s.State, failure: s.FailureReason, stop: s.StopRequestReference, end: s.EndRequestReference, reason: s.StopReason}
	if s.HarnessProcess != nil {
		snapshot.process = *s.HarnessProcess
	}
	if s.Events != nil {
		snapshot.events = s.Events.Len()
	}
	return snapshot
}

func (w workSnapshot) equal(s *AgentWork) bool {
	now := s.snapshot()
	return w.state == now.state && w.failure == now.failure && w.stop == now.stop && w.end == now.end && w.reason == now.reason && w.events == now.events && w.process.Equal(now.process)
}

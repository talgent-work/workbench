package domain

import (
	"context"
	"fmt"
	"strings"
)

// RequestExecutionEnd retains a normal stop obligation for the admitted start.
// Startup must still establish readiness and a running Turn must end before
// Stop is admitted; ending a batch never permanently closes its conversation.
func (s *AgentWork) RequestExecutionEnd(ctx context.Context, requestID string, port ExecutionPort) error {
	if s.HarnessProcess == nil || strings.TrimSpace(requestID) == "" || port == nil {
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
		return s.stop(ctx, requestID, StopReasonCompleted, port)
	case WorkFailed:
		if s.HarnessProcess.ended() {
			return nil
		}
		if s.EndRequestReference == "" {
			s.EndRequestReference = requestID
		}
		if s.StopRequestReference == "" {
			s.StopRequestReference = s.EndRequestReference
		}
		if !s.HarnessProcess.Prepared {
			if err := s.startExecution(ctx, port); err != nil {
				return executionRequestError(err)
			}
		}
		return executionRequestError(port.StopExecution(ctx, s.ID, s.StopRequestReference))
	case WorkStopping, WorkStopped, WorkClosing, WorkClosed:
		// Existing completion/reclamation obligations remain with Agent. A second
		// business completion request does not replace the admitted control identity.
		return nil
	default:
		return ErrWorkTransition
	}
}

// Stop admits a user or caller stop only between Turns (Stop between Turns);
// repeating an admitted stop confirms it.
func (s *AgentWork) Stop(ctx context.Context, requestID string, port ExecutionPort) error {
	if s.WaitsForDesk() {
		if strings.TrimSpace(requestID) == "" {
			return ErrInvalidWork
		}
		s.withdrawWaitingStart(requestID)
		return nil
	}
	if stopped, err := s.stopBeyondReach(ctx, requestID, StopReasonUserStopped, port); stopped || err != nil {
		return err
	}
	if s.processing() && s.HarnessProcess != nil && s.HarnessProcess.Turn.Active {
		return ErrTurnActive
	}
	return s.stop(ctx, requestID, StopReasonUserStopped, port)
}

func (s *AgentWork) stop(ctx context.Context, requestID string, reason StopReason, port ExecutionPort) error {
	if !s.processing() && !(s.State == WorkStopping && requestID == s.StopRequestReference) {
		return ErrWorkTransition
	}
	if strings.TrimSpace(requestID) == "" || port == nil {
		return ErrInvalidWork
	}
	s.admitStop(requestID, reason)
	return executionRequestError(port.StopExecution(ctx, s.ID, requestID))
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

// ReconcileProcess applies the environment authority's current observation of
// the Work's Harness process. A live or not-yet-live process leaves the Work
// unchanged; only its HarnessExit, a lost environment or the report that the
// start launched none and no longer can ends it.
func (s *AgentWork) ReconcileProcess(ctx context.Context, observation ProcessObservation, port ExecutionPort) error {
	p := s.HarnessProcess
	if p == nil || s.State == WorkClosed {
		return nil
	}
	switch observation.State {
	case ProcessPending:
		return nil
	case ProcessRunning:
		if observation.Ready {
			if err := s.ConfirmPrepared(ctx, observation.ObservedAt, port); err != nil {
				return err
			}
			return s.ConfirmStarted(ctx, observation.ObservedAt, port)
		}
		return nil
	case ProcessStartRejected:
		return s.confirmStartRejected(ctx, port)
	case ProcessStartAbandoned:
		return s.confirmStartAbandoned(ctx, port)
	case ProcessEnvironmentLost:
		return s.confirmEnvironmentLost(observation.LossReason)
	default:
		return ErrInvalidWork
	}
}

// RecordExit applies the acknowledged exit fact of the Work's current Harness
// process under the Lifecycle observation rule: an exit after readiness while
// a stop is admitted is normal, whatever its code; every other exit is a
// failure that keeps its code and reason. Either way compute release follows.
func (s *AgentWork) RecordExit(ctx context.Context, exit HarnessExit, port ExecutionPort) error {
	p := s.HarnessProcess
	if p == nil || s.State == WorkClosed || p.ended() || p.EnvironmentLost {
		return nil
	}
	if port == nil {
		return ErrInvalidWork
	}
	ready := p.Prepared && p.Started
	if !ready || s.StopRequestReference == "" {
		// A failure already recorded for this start keeps explaining it; the
		// exit code is retained either way.
		reason := s.FailureReason
		if reason == "" {
			reason = strings.TrimSpace(exit.Reason)
		}
		if reason == "" {
			reason = "native_process_exited_unexpectedly"
		}
		s.recordFailure(fmt.Sprintf("%s (exit code %d)", reason, exit.ExitCode))
	}
	p.Exited = true
	p.SubAgents = nil
	p.rest()
	if s.State == WorkStarting || s.processing() {
		s.State = WorkStopping
		s.StopReason = StopReasonCompleted
	}
	return executionRequestError(port.ReclaimCompute(ctx, s.ID))
}

// ConfirmComputeReclaimed applies the environment authority's release fact for
// the Work's terminated process obligation. Reclamation alone cannot prove
// exit, environment loss, rejection before external compute submission or
// abandonment of a start that launched no process; only the release of a run
// reclaimed beyond reach vouches for its exit.
func (s *AgentWork) ConfirmComputeReclaimed() error {
	p := s.HarnessProcess
	if p == nil || !(p.ended() || p.EnvironmentLost || p.Unreached) || p.ComputeReclaimed {
		return nil
	}
	if s.State != WorkStopping && s.State != WorkStopped && s.State != WorkFailed && s.State != WorkClosing && s.State != WorkClosed {
		return nil
	}
	p.ComputeReclaimed = true
	if p.Unreached && !p.Exited {
		// The released sandbox took with it the process the Work could no
		// longer reach (Disconnection release).
		p.Exited, p.SubAgents = true, nil
	}
	if !p.StartRejected {
		// A rejected start released its Desk when the rejection was confirmed.
		s.releaseDesk()
	}
	s.confirmTermination()
	return nil
}

// releaseDesk records that the run no longer holds its Desk's room, so its
// Computer can take the room back.
func (s *AgentWork) releaseDesk() {
	if p := s.HarnessProcess; p != nil && p.DeskID != "" && !p.holdsRoom() {
		s.record(WorkDeskReleased{WorkID: s.ID, DeskID: p.DeskID})
	}
}

// HoldsDesk reports whether the Work's current run holds the Desk.
func (s *AgentWork) HoldsDesk(deskID string) bool {
	p := s.HarnessProcess
	return p != nil && deskID != "" && p.DeskID == deskID && p.holdsRoom()
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

// confirmEnvironmentLost consumes authoritative permanent absence of the
// execution environment. It records failure and removal, never an exit; an
// already recorded exit keeps its normal completion semantics. A failure
// already recorded keeps explaining it; otherwise lossReason does, when the
// authority knows why the environment was lost.
func (s *AgentWork) confirmEnvironmentLost(lossReason string) error {
	p := s.HarnessProcess
	if p.EnvironmentLost || (p.ComputeReclaimed && s.State == WorkClosed) {
		return nil
	}
	if p.Exited {
		return s.ConfirmComputeReclaimed()
	}
	if s.State == WorkStopped {
		return nil
	}
	reason := s.FailureReason
	if reason == "" {
		reason = strings.TrimSpace(lossReason)
	}
	if reason == "" {
		reason = "execution environment lost"
	}
	s.recordFailure(reason)
	p.ComputeReclaimed = true
	p.EnvironmentLost = true
	p.SubAgents = nil
	p.rest()
	s.releaseDesk()
	s.confirmTermination()
	return nil
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

func executionRequestError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrExecutionRequest, err)
}

// confirmStartRejected applies the execution authority's proof that an admitted
// start cannot launch and holds no compute. Retain that startup fact instead
// of inventing a native exit or permanent loss of an existing environment.
func (s *AgentWork) confirmStartRejected(ctx context.Context, port ExecutionPort) error {
	p := s.HarnessProcess
	if p.StartRejected {
		return nil
	}
	if p.Prepared || p.Started || p.Exited || p.EnvironmentLost || port == nil {
		return ErrInvalidWork
	}
	p.StartRejected = true
	s.releaseDesk()
	s.recordFailure("The execution environment could not be created for this start.")
	return executionRequestError(port.ReclaimCompute(ctx, s.ID))
}

// confirmStartAbandoned applies the execution authority's report that the
// start never launched a Harness process and, past its Startup Deadline, no
// longer can. Nothing is left to stop, so the environment is released at once
// under the Abandoned start rule. A start still Starting is failed by its
// deadline first; one that ran a process ends by its exit instead.
func (s *AgentWork) confirmStartAbandoned(ctx context.Context, port ExecutionPort) error {
	p := s.HarnessProcess
	if p.StartAbandoned || p.StartRejected || p.EnvironmentLost {
		return nil
	}
	if port == nil {
		return ErrInvalidWork
	}
	// A start known to have become ready or exited ends by its exit; a
	// contradicting report is not applied, and it must not block recovery.
	if p.Started || p.Exited || (s.State != WorkFailed && s.State != WorkClosing) {
		return nil
	}
	p.StartAbandoned = true
	return executionRequestError(port.ReclaimCompute(ctx, s.ID))
}

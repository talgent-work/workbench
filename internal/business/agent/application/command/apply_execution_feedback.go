package command

import (
	"context"
	"errors"
	"strings"
	"time"

	integrationmessage "github.com/go-jimu/components/ddd/message"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type ExecutionFeedbackKind string

const (
	ProcessObserved         ExecutionFeedbackKind = "process_observed"
	ExecutorPrepared        ExecutionFeedbackKind = "prepared"
	AgentStarted            ExecutionFeedbackKind = "started"
	UserInteractionRequired ExecutionFeedbackKind = "user_interaction_required"
	UserInteractionResolved ExecutionFeedbackKind = "user_interaction_resolved"
	ExecutionExited         ExecutionFeedbackKind = "exited"
	ComputeReclaimed        ExecutionFeedbackKind = "compute_reclaimed"
	// EnvironmentLost is the authority's finding that the Work's current
	// environment is permanently gone; its emitter vouches that it is current.
	EnvironmentLost     ExecutionFeedbackKind = "environment_lost"
	TurnStarted         ExecutionFeedbackKind = "turn_started"
	TurnFinished        ExecutionFeedbackKind = "turn_finished"
	InputSettled        ExecutionFeedbackKind = "input_settled"
	WorkProgressUpdated ExecutionFeedbackKind = "work_progress_updated"
	SubAgentsUpdated    ExecutionFeedbackKind = "subagents_updated"
	// PermissionModeObserved is the permission mode the process reports it
	// now applies.
	PermissionModeObserved ExecutionFeedbackKind = "permission_mode_observed"
	TurnActivity           ExecutionFeedbackKind = "turn_activity"
	// AttachmentLost and AttachmentReturned are Dispatcher's loss and return
	// of its attachment to the process's sandbox, the observation a Cloud
	// Work's Disconnection follows; DetachedSince names the loss.
	AttachmentLost     ExecutionFeedbackKind = "attachment_lost"
	AttachmentReturned ExecutionFeedbackKind = "attachment_returned"
)

var ErrUnknownExecutionFeedback = errors.New("agent: unknown execution feedback")

// ExecutionFeedback is a hint about a Work's Harness process, positioned in its
// Harness Output Stream. An exit fact names its process instead.
type ExecutionFeedback struct {
	// ProcessID, ExitCode and ExitReason form an exit fact.
	ProcessID              string
	ExitCode               int
	ExitReason             string
	RequiredInteractionIDs []string
	WorkProgress           []domain.WorkProgressItem
	SubAgents              []domain.SubAgent
	// PermissionMode is the mode a PermissionModeObserved report names.
	PermissionMode      domain.PermissionMode
	Kind                ExecutionFeedbackKind
	WorkID              string
	InteractionID       string
	ObservationSequence int64
	TurnCompleted       bool
	// InputID names a settled input, or the interaction a settled reply answered.
	InputID string
	// InputOutcome is how the input settled; empty for a settled reply.
	InputOutcome InputOutcome
	// DetachedSince is when the attachment an AttachmentLost or
	// AttachmentReturned report is about was lost.
	DetachedSince time.Time
}

// ProcessObservationPort supplies the environment authority's current
// observation of the Work's Harness process, and records the acknowledgement
// of an exit fact for the process it names.
type ProcessObservationPort interface {
	ObserveProcess(ctx context.Context, workID string) (WorkProcessObservation, error)
	// IsCurrentProcess reports whether the process is the Work's current one.
	IsCurrentProcess(ctx context.Context, workID, processID string) (bool, error)
	// AcknowledgeExit records that the named process exited, reporting false
	// when it is not the process of the Work's current allocation.
	AcknowledgeExit(ctx context.Context, workID, processID string) (bool, error)
}

type ApplyExecutionFeedbackHandler struct {
	buffer      RunningHarnessPort
	settlements integrationmessage.Publisher
	sessions    domain.AgentWorkRepository
	processes   ProcessObservationPort
	execution   domain.ExecutionPort
	now         func() time.Time
}

func NewApplyExecutionFeedbackHandler(now func() time.Time, sessions domain.AgentWorkRepository, processes ProcessObservationPort, execution domain.ExecutionPort, buffer RunningHarnessPort, settlements integrationmessage.Publisher) *ApplyExecutionFeedbackHandler {
	return &ApplyExecutionFeedbackHandler{now: now, sessions: sessions, processes: processes, execution: execution, buffer: buffer, settlements: settlements}
}

func (h *ApplyExecutionFeedbackHandler) Handle(ctx context.Context, feedback ExecutionFeedback) error {
	if strings.TrimSpace(feedback.WorkID) == "" {
		return domain.ErrInvalidWork
	}
	switch feedback.Kind {
	case ProcessObserved, EnvironmentLost, ComputeReclaimed, ExecutionExited:
	default:
		// Positions order one process's stream; another process's late
		// feedback must not shadow the current one's (feedback ordering).
		if strings.TrimSpace(feedback.ProcessID) == "" {
			return domain.ErrInvalidWork
		}
		current, err := h.processes.IsCurrentProcess(ctx, feedback.WorkID, feedback.ProcessID)
		if err != nil {
			return err
		}
		if !current {
			// An exiting process's input still settled in the buffer: its
			// caller is told, though the Work's state follows the current one.
			return h.reportSettlement(ctx, feedback)
		}
	}
	session, err := h.sessions.Get(ctx, feedback.WorkID)
	if err != nil {
		return err
	}
	previousState := session.State
	var previousProcess domain.HarnessProcess
	if p := session.HarnessProcess; p != nil {
		previousProcess = *p
	}
	var observation WorkProcessObservation
	switch feedback.Kind {
	case ExecutionExited:
		if session.HarnessProcess == nil || session.HarnessProcess.ComputeReclaimed {
			return nil
		}
		// An exit fact of another process changes nothing (Lifecycle observation).
		current, err := h.processes.AcknowledgeExit(ctx, feedback.WorkID, feedback.ProcessID)
		if err != nil || !current {
			return err
		}
	case ComputeReclaimed, EnvironmentLost, ProcessObserved:
		if session.HarnessProcess == nil || session.HarnessProcess.ComputeReclaimed {
			return nil
		}
		if feedback.Kind == EnvironmentLost {
			// The environment authority already found the live allocation's
			// sandbox permanently absent; that is the observation. Observing a
			// live allocation asks only Dispatcher, which can never
			// report the loss of a sandbox it no longer reaches.
			observation.ProcessObservation = domain.ProcessObservation{State: domain.ProcessEnvironmentLost, ComputeReclaimed: true}
		} else {
			// Retain the Work version before observing. Concurrent stop/resume makes
			// Save reject this observation before any outbound resource request.
			var err error
			if observation, err = h.processes.ObserveProcess(ctx, feedback.WorkID); err != nil {
				return err
			}
		}
		observation.ObservedAt = h.now()
	}
	requests := deferExecution(h.execution)
	switch feedback.Kind {
	case ExecutorPrepared:
		if err = session.ConfirmPrepared(ctx, h.now(), requests); err == nil {
			err = h.recordIdleness(ctx, session)
		}
	case AgentStarted:
		if err = session.ConfirmStarted(ctx, h.now(), requests); err == nil {
			err = h.recordIdleness(ctx, session)
		}
	case UserInteractionRequired, UserInteractionResolved:
		if feedback.Kind == UserInteractionResolved && strings.TrimSpace(feedback.InteractionID) == "" {
			return domain.ErrInvalidWork
		}
		// A reply accepted after Dispatcher observed the request is in the
		// buffer by now; it answers the request (Response acceptance).
		var buffered BufferedInputs
		buffered, err = h.buffer.ReadBuffer(ctx, session.ID)
		required := buffered.Unanswered(feedback.RequiredInteractionIDs)
		if err == nil {
			err = session.ApplyResponseRequirement(feedback.ObservationSequence, required, h.now())
		}
		if err == nil && !required && feedback.Kind == UserInteractionResolved {
			err = session.RecordIdleness(buffered.Pending(), h.now())
		}
	case ExecutionExited:
		err = session.RecordExit(ctx, domain.HarnessExit{ExitCode: feedback.ExitCode, Reason: feedback.ExitReason}, requests)
	case EnvironmentLost, ComputeReclaimed, ProcessObserved:
		err = session.ReconcileProcess(ctx, observation.ProcessObservation, requests)
		if err == nil && observation.State == domain.ProcessRunning {
			err = h.reconcileNativePhase(ctx, session, observation)
		}
		if err == nil && observation.ComputeReclaimed {
			err = session.ConfirmComputeReclaimed()
		}
		if err == nil && (observation.State == domain.ProcessRunning || observation.State == domain.ProcessPending) && session.State == previousState && session.HarnessProcess.Equal(previousProcess) {
			return nil
		}
	case TurnStarted, TurnFinished:
		var buffered BufferedInputs
		buffered, err = h.buffer.ReadBuffer(ctx, session.ID)
		if err == nil {
			err = session.ObserveTurn(domain.TurnObservation{Sequence: feedback.ObservationSequence, Active: feedback.Kind == TurnStarted, Completed: feedback.TurnCompleted}, buffered.Pending(), buffered.Unanswered(feedback.RequiredInteractionIDs), h.now())
		}
		if err == nil && feedback.Kind == TurnFinished {
			// A stop, end or closure waiting for the Turn proceeds now
			// (Stop between Turns).
			_, err = session.RecoverExecution(ctx, requests)
		}
	case WorkProgressUpdated:
		err = session.RecordWorkProgress(feedback.ObservationSequence, feedback.WorkProgress, h.now())
	case SubAgentsUpdated:
		err = session.RecordSubAgents(feedback.ObservationSequence, feedback.SubAgents)
	case PermissionModeObserved:
		err = session.RecordPermissionMode(feedback.ObservationSequence, feedback.PermissionMode)
	case InputSettled:
		if strings.TrimSpace(feedback.InputID) == "" {
			return domain.ErrInvalidWork
		}
		err = h.recordIdleness(ctx, session)
	case TurnActivity:
		if !session.ObserveTurnActivity() {
			return nil
		}
	case AttachmentLost, AttachmentReturned:
		var changed bool
		if changed, err = h.applyAttachment(ctx, session, feedback, requests); err == nil && !changed {
			return nil
		}
	default:
		return ErrUnknownExecutionFeedback
	}
	if err != nil {
		return err
	}
	if err := h.sessions.Save(ctx, session); err != nil {
		return err
	}
	if err := requests.send(ctx); err != nil {
		return err
	}
	return h.reportSettlement(ctx, feedback)
}

// applyAttachment records or ends a Cloud Work's Disconnection from
// Dispatcher's attachment to its sandbox, and stops it beyond reach when the
// loss has lasted the Disconnection timeout. The report names the current
// process, and a return ends only the loss it follows, never a later one. A
// Managed Work's Disconnection follows its Computer's Presence instead.
func (h *ApplyExecutionFeedbackHandler) applyAttachment(ctx context.Context, session *domain.AgentWork, feedback ExecutionFeedback, requests *deferredExecution) (bool, error) {
	if session.OnManagedComputer() {
		return false, nil
	}
	if feedback.DetachedSince.IsZero() {
		return false, domain.ErrInvalidWork
	}
	if feedback.Kind == AttachmentReturned {
		return session.EndDisconnectionOf(feedback.DetachedSince), nil
	}
	recorded, err := session.RecordDisconnection(feedback.DetachedSince)
	if err != nil {
		return false, err
	}
	// A Cloud Work stops beyond reach into Stopping, and no reconnection
	// owes it a Resume.
	stopped, err := session.StopBeyondReach(ctx, h.now(), false, requests)
	return recorded || stopped, err
}

// reportSettlement tells the input's caller that it settled, for settlement
// feedback that carries an outcome.
func (h *ApplyExecutionFeedbackHandler) reportSettlement(ctx context.Context, feedback ExecutionFeedback) error {
	if feedback.Kind != InputSettled || feedback.InputOutcome == "" || strings.TrimSpace(feedback.InputID) == "" {
		return nil
	}
	return publishInputSettlement(ctx, h.settlements, InputSettlement{WorkID: feedback.WorkID, InputID: feedback.InputID, Outcome: feedback.InputOutcome, At: h.now()})
}

// recordIdleness supplies the current accepted-input fact after readiness or
// settlement facts; the Work decides whether nothing is left to process.
func (h *ApplyExecutionFeedbackHandler) recordIdleness(ctx context.Context, session *domain.AgentWork) error {
	buffered, err := h.buffer.ReadBuffer(ctx, session.ID)
	if err != nil {
		return err
	}
	return session.RecordIdleness(buffered.Pending(), h.now())
}

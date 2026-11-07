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
	TurnStarted             ExecutionFeedbackKind = "turn_started"
	TurnFinished            ExecutionFeedbackKind = "turn_finished"
	InputSettled            ExecutionFeedbackKind = "input_settled"
	WorkProgressUpdated     ExecutionFeedbackKind = "work_progress_updated"
	SubAgentsUpdated        ExecutionFeedbackKind = "subagents_updated"
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

type ApplyExecutionFeedbackHandler struct {
	buffer      RunningHarnessPort
	settlements integrationmessage.Publisher
	sessions    domain.AgentWorkRepository
	executions  domain.AgentExecutionRepository
	reports     *ExecutionCoordination
	now         func() time.Time
}

func NewApplyExecutionFeedbackHandler(now func() time.Time, sessions domain.AgentWorkRepository, executions domain.AgentExecutionRepository, reports *ExecutionCoordination, buffer RunningHarnessPort, settlements integrationmessage.Publisher) *ApplyExecutionFeedbackHandler {
	return &ApplyExecutionFeedbackHandler{now: now, sessions: sessions, executions: executions, reports: reports, buffer: buffer, settlements: settlements}
}

func (h *ApplyExecutionFeedbackHandler) Handle(ctx context.Context, feedback ExecutionFeedback) error {
	if strings.TrimSpace(feedback.WorkID) == "" {
		return domain.ErrInvalidWork
	}
	if feedback.Kind != ProcessObserved && strings.TrimSpace(feedback.ProcessID) == "" {
		// Positions order one process's stream, and an exit fact names its
		// process; feedback without one is no fact (feedback ordering).
		return domain.ErrInvalidWork
	}
	execution, err := h.executions.Latest(ctx, feedback.WorkID)
	if err != nil && !errors.Is(err, domain.ErrExecutionNotFound) {
		return err
	}
	switch feedback.Kind {
	case ExecutionExited:
		return h.recordExit(ctx, execution, feedback)
	case ExecutorPrepared, AgentStarted:
		if !launched(execution, feedback.ProcessID) {
			return nil
		}
		return h.confirmReady(ctx, execution.ID, domain.ReadinessReport{Prepared: feedback.Kind == ExecutorPrepared, Started: feedback.Kind == AgentStarted})
	case ProcessObserved:
		return h.observeProcess(ctx, execution)
	}
	if !launched(execution, feedback.ProcessID) || execution.Retired() {
		// An exiting process's input still settled in the buffer: its
		// caller is told, though the Work's state follows the current one.
		return h.reportSettlement(ctx, feedback)
	}
	session, err := h.sessions.Get(ctx, feedback.WorkID)
	if err != nil {
		return err
	}
	switch feedback.Kind {
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
	case TurnStarted, TurnFinished:
		var buffered BufferedInputs
		buffered, err = h.buffer.ReadBuffer(ctx, session.ID)
		if err == nil {
			err = session.ObserveTurn(domain.TurnObservation{Sequence: feedback.ObservationSequence, Active: feedback.Kind == TurnStarted, Completed: feedback.TurnCompleted}, buffered.Pending(), buffered.Unanswered(feedback.RequiredInteractionIDs), h.now())
		}
		if err == nil && feedback.Kind == TurnFinished {
			// A stop, end or closure waiting for the Turn proceeds now
			// (Stop between Turns).
			_, err = session.RecoverExecution()
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
		if changed, err = h.applyAttachment(session, feedback); err == nil && !changed {
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
	return h.reportSettlement(ctx, feedback)
}

// launched reports whether the feedback names the execution's launched
// process.
func launched(execution *domain.AgentExecution, processID string) bool {
	return execution != nil && processID != "" && execution.Process.ID == processID
}

// recordExit acknowledges an exit fact on the execution whose launched
// process it names; its report reaches the Work once saved. A fact naming
// another process changes nothing (Foreign exit), and either way it is
// acknowledged, so Dispatcher stops redelivering it.
func (h *ApplyExecutionFeedbackHandler) recordExit(ctx context.Context, execution *domain.AgentExecution, feedback ExecutionFeedback) error {
	if !launched(execution, feedback.ProcessID) {
		return nil
	}
	if _, changed := execution.RecordExit(feedback.ProcessID, domain.HarnessExit{ExitCode: feedback.ExitCode, Reason: feedback.ExitReason}); !changed {
		return nil
	}
	if err := h.executions.Save(ctx, execution); err != nil {
		return err
	}
	return h.reports.ApplyExecutionReports(ctx, feedback.WorkID)
}

// confirmReady applies a readiness report to the execution; its ready report
// reaches the Work once saved.
func (h *ApplyExecutionFeedbackHandler) confirmReady(ctx context.Context, executionID string, report domain.ReadinessReport) error {
	execution, err := h.executions.Get(ctx, executionID)
	if err != nil {
		return err
	}
	changed, err := execution.ConfirmReady(report, h.now())
	if err != nil || !changed {
		return err
	}
	if err := h.executions.Save(ctx, execution); err != nil {
		return err
	}
	return h.reports.ApplyExecutionReports(ctx, execution.WorkID)
}

// observeProcess applies Dispatcher's current observation of the launched
// process: its readiness to the execution, its Turn and required responses to
// the Work, which restores the Work's phase after lost feedback.
func (h *ApplyExecutionFeedbackHandler) observeProcess(ctx context.Context, execution *domain.AgentExecution) error {
	if execution == nil || execution.Process.ID == "" || execution.Process.Exited || execution.Retired() {
		return nil
	}
	observation, err := h.buffer.ObserveProcess(ctx, execution.WorkID, execution.Process.ID)
	if err != nil || !observation.Running {
		return err
	}
	if observation.Ready {
		if err := h.confirmReady(ctx, execution.ID, domain.ReadinessReport{Prepared: true, Started: true}); err != nil {
			return err
		}
	}
	session, err := h.sessions.Get(ctx, execution.WorkID)
	if err != nil {
		return err
	}
	previousState := session.State
	previousProcess := *session.HarnessProcess
	if err := h.reconcileNativePhase(ctx, session, observation); err != nil {
		return err
	}
	if session.State == previousState && session.HarnessProcess.Equal(previousProcess) {
		return nil
	}
	return h.sessions.Save(ctx, session)
}

// applyAttachment records or ends a Cloud Work's Disconnection from
// Dispatcher's attachment to its sandbox, and stops it beyond reach when the
// loss has lasted the Disconnection timeout. The report names the current
// process, and a return ends only the loss it follows, never a later one. A
// Managed Work's Disconnection follows its Computer's Presence instead.
func (h *ApplyExecutionFeedbackHandler) applyAttachment(session *domain.AgentWork, feedback ExecutionFeedback) (bool, error) {
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
	stopped := session.StopBeyondReach(h.now(), false)
	return recorded || stopped, nil
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

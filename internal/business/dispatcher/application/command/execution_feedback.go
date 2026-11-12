package command

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalidExecutionFeedback     = errors.New("dispatcher: invalid execution feedback")
	ErrExecutionFeedbackRejected    = errors.New("dispatcher: execution feedback rejected")
	ErrExecutionFeedbackUnavailable = errors.New("dispatcher: execution feedback unavailable")
)

// ExecutionObservation is feedback about a Work's Harness process. It names the
// process and is positioned in its Harness Output Stream, so a replayed
// repetition is harmless and an earlier process's late feedback is ignored. An
// exit fact also carries the exit code and the failure Dispatcher observed;
// Agent classifies it and acknowledges it.
type ExecutionObservation struct {
	LoadedPlugins          []LoadedPlugin
	RequiredInteractionIDs []string
	WorkProgress           []WorkProgressItem
	SubAgents              []SubAgent
	WorkID                 string
	Kind                   ExecutionObservationKind
	InteractionID          string
	ObservationSequence    int64
	TurnCompleted          bool
	InputID                string
	// InputOutcome is how a buffered entry settled, for ExecutionInputSettled.
	InputOutcome InputOutcome
	ProcessID    string
	ExitCode     int
	// ExitReason is a native diagnostic of the exit, such as a rejected
	// initialization; Agent decides whether the exit was normal.
	ExitReason string
	// PermissionMode is the native mode an ExecutionPermissionModeObserved
	// report names.
	PermissionMode string
	// DetachedSince is when the attachment an ExecutionAttachmentLost or
	// ExecutionAttachmentReturned report is about was lost.
	DetachedSince time.Time
}

type ExecutionObservationKind string

const (
	ExecutionPrepared                ExecutionObservationKind = "prepared"
	ExecutionStarted                 ExecutionObservationKind = "started"
	ExecutionUserInteractionRequired ExecutionObservationKind = "user_interaction_required"
	ExecutionUserInteractionResolved ExecutionObservationKind = "user_interaction_resolved"
	ExecutionExited                  ExecutionObservationKind = "exited"
	ExecutionTurnStarted             ExecutionObservationKind = "turn_started"
	ExecutionTurnFinished            ExecutionObservationKind = "turn_finished"
	ExecutionInputSettled            ExecutionObservationKind = "input_settled"
	ExecutionWorkProgressUpdated     ExecutionObservationKind = "work_progress_updated"
	ExecutionSubAgentsUpdated        ExecutionObservationKind = "subagents_updated"
	ExecutionPluginLoadsObserved     ExecutionObservationKind = "plugin_loads_observed"
	// ExecutionPermissionModeObserved reports the permission mode the process
	// now applies, whenever it reports a different one.
	ExecutionPermissionModeObserved ExecutionObservationKind = "permission_mode_observed"
	// ExecutionTurnActivity reports that the Work's Turn progresses in its
	// Harness Output Stream, at most once a minute.
	ExecutionTurnActivity ExecutionObservationKind = "turn_activity"
	// ExecutionAttachmentLost reports that Dispatcher lost its attachment to
	// the process's sandbox and could not attach again.
	ExecutionAttachmentLost ExecutionObservationKind = "attachment_lost"
	// ExecutionAttachmentReturned reports that Dispatcher attached again
	// after a reported loss.
	ExecutionAttachmentReturned ExecutionObservationKind = "attachment_returned"
)

// InputOutcome is how a buffered input or reply settled.
type InputOutcome string

const (
	// InputCompleted: the Harness finished the input or resolved the replied interaction.
	InputCompleted InputOutcome = "completed"
	// InputClosed: the replied interaction no longer exists.
	InputClosed InputOutcome = "closed"
	// InputAbandoned: the process exited before finishing the written input.
	InputAbandoned InputOutcome = "abandoned"
)

type LoadedPlugin struct{ Slug, Version string }

type WorkProgressItem struct {
	Content, Status, ActiveForm string
}

// SubAgent is one SubAgent the main Agent of the process delegated to, with
// the state its Harness last reported: running, completed, failed or stopped.
type SubAgent struct {
	ID, Type, Task, Status string
}

// ExecutionFeedbackReporter delivers observations to Agent; a nil error is
// Agent's acknowledgement. Compute removal is reported by the sandbox
// integration, not Dispatcher.
type ExecutionFeedbackReporter interface {
	ReportExecution(context.Context, ExecutionObservation) error
	// ReportConnectionEnded reports that the sandbox side of a still running
	// process's connection failed. Agent decides whether to connect again; the
	// report establishes no process exit.
	ReportConnectionEnded(ctx context.Context, workID, processID string) error
}

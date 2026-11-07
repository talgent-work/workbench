package domain

import "time"

// AgentWork changes its AgentExecution only
// through these. Each names the start it concerns by its admission time.
const (
	EventKindWorkStartAdmitted           EventKind = "agent.work.start_admitted"
	EventKindWorkStopRequested           EventKind = "agent.work.stop_requested"
	EventKindWorkComputeReleaseRequested EventKind = "agent.work.compute_release_requested"
)

// Execution reports (Execution reports rule): an AgentExecution reports what
// happened to its run and never changes its Work's lifecycle.
const (
	EventKindExecutionReady           EventKind = "agent.execution.ready"
	EventKindExecutionStartFailed     EventKind = "agent.execution.start_failed"
	EventKindExecutionStartRejected   EventKind = "agent.execution.start_rejected"
	EventKindExecutionStartAbandoned  EventKind = "agent.execution.start_abandoned"
	EventKindExecutionEnvironmentLost EventKind = "agent.execution.environment_lost"
	EventKindExecutionExited          EventKind = "agent.execution.exited"
	EventKindExecutionReleased        EventKind = "agent.execution.released"
)

// WorkStartAdmitted records an admitted start: its Desk on the Work's
// Computer, seated or waiting in its line. Its AgentExecution begins.
type WorkStartAdmitted struct {
	WorkID          string
	StartAdmittedAt time.Time
	DeskID          string
	Waiting         bool
}

func (WorkStartAdmitted) Kind() EventKind { return EventKindWorkStartAdmitted }
func (e WorkStartAdmitted) Key() string   { return e.WorkID }

// WorkStopRequested records that the Work asks its Harness process to exit
// under its stop identity, with a Waiting Turn cancelled first when asked.
type WorkStopRequested struct {
	WorkID            string
	StartAdmittedAt   time.Time
	StopRequestID     string
	CancelWaitingTurn bool
}

func (WorkStopRequested) Kind() EventKind { return EventKindWorkStopRequested }
func (e WorkStopRequested) Key() string   { return e.WorkID }

// WorkComputeReleaseRequested records that the Work's execution ended by
// Cause, so its AgentExecution retires and releases what it owns.
type WorkComputeReleaseRequested struct {
	WorkID          string
	StartAdmittedAt time.Time
	Cause           RetirementCause
}

func (WorkComputeReleaseRequested) Kind() EventKind { return EventKindWorkComputeReleaseRequested }
func (e WorkComputeReleaseRequested) Key() string   { return e.WorkID }

// ExecutionReady reports that the run became ready before its Startup
// Deadline.
type ExecutionReady struct {
	WorkID          string
	StartAdmittedAt time.Time
}

func (ExecutionReady) Kind() EventKind { return EventKindExecutionReady }
func (e ExecutionReady) Key() string   { return e.WorkID }

// ExecutionStartFailed reports that the start missed its Startup Deadline;
// Reason names the earliest incomplete Required Startup Step.
type ExecutionStartFailed struct {
	WorkID          string
	StartAdmittedAt time.Time
	Reason          string
}

func (ExecutionStartFailed) Kind() EventKind { return EventKindExecutionStartFailed }
func (e ExecutionStartFailed) Key() string   { return e.WorkID }

// ExecutionStartRejected reports that the environment's creation was
// definitely refused or failed while nothing was bound.
type ExecutionStartRejected struct {
	WorkID          string
	StartAdmittedAt time.Time
}

func (ExecutionStartRejected) Kind() EventKind { return EventKindExecutionStartRejected }
func (e ExecutionStartRejected) Key() string   { return e.WorkID }

// ExecutionStartAbandoned reports that the start never launched a Harness
// process and no longer can.
type ExecutionStartAbandoned struct {
	WorkID          string
	StartAdmittedAt time.Time
}

func (ExecutionStartAbandoned) Kind() EventKind { return EventKindExecutionStartAbandoned }
func (e ExecutionStartAbandoned) Key() string   { return e.WorkID }

// ExecutionEnvironmentLost reports that the bound environment is no longer
// observed; Reason explains it when the environment authority knows why.
type ExecutionEnvironmentLost struct {
	WorkID          string
	StartAdmittedAt time.Time
	Reason          string
}

func (ExecutionEnvironmentLost) Kind() EventKind { return EventKindExecutionEnvironmentLost }
func (e ExecutionEnvironmentLost) Key() string   { return e.WorkID }

// ExecutionExited reports the acknowledged exit of the launched process;
// Ready tells whether the run had become ready.
type ExecutionExited struct {
	WorkID          string
	StartAdmittedAt time.Time
	Exit            HarnessExit
	Ready           bool
}

func (ExecutionExited) Kind() EventKind { return EventKindExecutionExited }
func (e ExecutionExited) Key() string   { return e.WorkID }

// ExecutionReleasedEvent is the ExecutionReleased Domain Event: nothing the
// execution owned remains. The type is named apart from the Released state.
type ExecutionReleasedEvent struct {
	WorkID          string
	StartAdmittedAt time.Time
}

func (ExecutionReleasedEvent) Kind() EventKind { return EventKindExecutionReleased }
func (e ExecutionReleasedEvent) Key() string   { return e.WorkID }

// Reports rebuilds, from its facts, every report the execution made, in the
// order it made them, so a Work that missed one applies it again (Apply
// execution report, Confirm compute release).
func (e *AgentExecution) Reports() []Event {
	var reports []Event
	if e.Ready() && e.Outcome.StartFailure == "" {
		reports = append(reports, ExecutionReady{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	}
	if e.Outcome.StartFailure != "" {
		reports = append(reports, ExecutionStartFailed{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: e.Outcome.StartFailure})
	}
	if e.Outcome.Rejected {
		reports = append(reports, ExecutionStartRejected{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	}
	if e.Outcome.Abandoned {
		reports = append(reports, ExecutionStartAbandoned{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	}
	if e.Process.Exited {
		reports = append(reports, e.exitReport())
	}
	if e.Outcome.Lost {
		reports = append(reports, ExecutionEnvironmentLost{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Reason: e.Outcome.LossReason})
	}
	if e.State == ExecutionReleased {
		reports = append(reports, ExecutionReleasedEvent{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt})
	}
	return reports
}

func (e *AgentExecution) exitReport() ExecutionExited {
	return ExecutionExited{WorkID: e.WorkID, StartAdmittedAt: e.StartAdmittedAt, Exit: e.Process.Exit, Ready: e.Ready() && e.Outcome.StartFailure == ""}
}

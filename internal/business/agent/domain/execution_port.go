package domain

import (
	"context"
	"time"
)

// StartExecutionRequest is what one admitted start asks its Computer's
// execution realization to run.
type StartExecutionRequest struct {
	RepositorySelection    WorkRepositorySelection
	AuthorizationSelection WorkAuthorizationSelection
	ThinkingLevel          string
	// PermissionMode is a Managed Work's; empty on a Cloud, which bypasses.
	PermissionMode PermissionMode
	WorkModel      WorkModelSelection
	AgentID        string
	WorkID         string
	// ComputerID is the Work's Computer; the execution port reaches it
	// through the realization of its kind.
	ComputerID string
	// ComputerName is what Computer calls the Work's Computer when the
	// execution port realizes the start, which fills it in; the run's
	// telemetry names its Computer by it. It is not part of the admitted
	// start, so a later rename changes no repeated registration.
	ComputerName string
	// ProjectID is the Work's Project; empty for a Work without one.
	ProjectID               string
	Capabilities            CapabilitySnapshot
	HarnessSessionReference string
	ContextMode             ContextMode
	// Soul is appended to the startup Agent's prompt, never replacing it, and
	// is not part of the Capability Snapshot.
	Soul string
	// PreparationBound is when the in-environment preparation of this start
	// (Plugin installation, then repository checkout) degrades.
	PreparationBound   time.Time
	WorkspaceReference string
	InputSource        WorkInputSource
	ReadOnlyMounts     []ReadOnlyMount
	HomeBinding        HomeBinding
	TraceAttributes    map[string]string
	// StartingContext is the context the Work started from, if any; a start
	// without a conversation of its own yet takes it up.
	StartingContext *StartingContext
}

// ExecutionPort reports request errors; a successful call never establishes
// readiness, exit or removal. Those facts are supplied to the Work separately.
// A Work has at most one Harness process, so the Work addresses it.
type ExecutionPort interface {
	StartExecution(context.Context, StartExecutionRequest) error
	StopExecution(ctx context.Context, workID, stopRequestID string) error
	// CancelRunningTurn asks the Harness to end the Turn it is running, if any; the
	// Work and its process keep running.
	CancelRunningTurn(ctx context.Context, workID string) error
	// ReclaimCompute requests release of the Work's environment after its
	// process exited, or in place of that exit for a Cloud run stopped beyond
	// reach.
	ReclaimCompute(ctx context.Context, workID string) error
	// RetireFencedRun retires what the platform keeps for a run its Managed
	// Computer fenced during a Disconnection: the fence vouches for exit and
	// release, so no compute is reclaimed, and the machine's reconciliation
	// cleans up what remains when it reconnects.
	RetireFencedRun(ctx context.Context, workID string) error
}

// ProcessState is the environment authority's current observation of the
// Work's Harness process, not a replayed feedback item. A process's exit is
// not observed here: it arrives as its HarnessExit.
type ProcessState string

const (
	// ProcessPending means the process of the current start is not live yet.
	ProcessPending ProcessState = "pending"
	ProcessRunning ProcessState = "running"
	// ProcessStartRejected confirms the admitted start cannot launch and no
	// external compute exists for it: none was submitted, or the provider
	// refused the creation. It is not a process exit.
	ProcessStartRejected ProcessState = "start_rejected"
	// ProcessEnvironmentLost means the Work's execution environment has
	// permanently disappeared.
	ProcessEnvironmentLost ProcessState = "environment_lost"
	// ProcessStartAbandoned means the start never launched a Harness process
	// and, past its Startup Deadline, no longer can; its environment still
	// exists until released.
	ProcessStartAbandoned ProcessState = "start_abandoned"
)

type ProcessObservation struct {
	// Ready means initialization was durably confirmed by the native authority.
	Ready bool
	// ComputeReclaimed is current environment absence, not a deletion receipt.
	ComputeReclaimed bool
	State            ProcessState
	// ObservedAt is when the observation was made; readiness it reports is
	// judged against the current start's Startup Deadline at that time.
	ObservedAt time.Time
	// LossReason explains a lost environment when the authority knows why,
	// such as the removal of its Computer.
	LossReason string
}

// HarnessExit is the acknowledged exit fact of the Work's current Harness
// process: its exit code and the failure Dispatcher observed, if any. It is a
// Value Object without process identity.
type HarnessExit struct {
	ExitCode int
	Reason   string
}

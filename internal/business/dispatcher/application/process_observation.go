package application

import "context"

// ProcessState is the owning replica's observation of a running native process.
type ProcessState string

const (
	// ProcessPending means Dispatcher has not attached to this process yet.
	ProcessPending ProcessState = "pending"
	ProcessRunning ProcessState = "running"
)

// ProcessObservation is how a running process stands. Its exit is not
// observed here: the exit fact reaches Agent as feedback.
type ProcessObservation struct {
	Ready         bool
	State         ProcessState
	Turn          TurnObservation
	PhaseSequence int64
	// RequiredInteractionIDs are the requests awaiting a reply; Agent tells
	// which already have a buffered one.
	RequiredInteractionIDs []string
	// Attached means the replica reads the process's output live from its
	// sandbox now.
	Attached bool
}

// TurnObservation contains actual root activity, excluding input delivery.
type TurnObservation struct {
	Sequence  int64
	Active    bool
	Completed bool
}

// ProcessObservationReader answers from the memory of the replica reading the
// Work's Harness Output Stream.
type ProcessObservationReader interface {
	ObserveProcess(ctx context.Context, workID, processID string) (ProcessObservation, error)
}

func (a *Application) ObserveProcess(ctx context.Context, workID, processID string) (ProcessObservation, error) {
	return a.processes.ObserveProcess(ctx, workID, processID)
}

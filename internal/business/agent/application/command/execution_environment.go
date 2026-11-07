package command

import "context"

// AllocationStep is the one step an allocation's onboarding is missing.
type AllocationStep int

const (
	// AllocationSettled needs nothing: a connected process, or none to connect.
	AllocationSettled AllocationStep = iota
	// AllocationReconcile advances submission, first delivery, credential
	// draining or removal from the allocation's durable facts.
	AllocationReconcile
	// AllocationPrepare sees environment preparation through to its exit and
	// then starts and connects the Harness.
	AllocationPrepare
	// AllocationReconnect hands a running Harness process to Dispatcher again.
	AllocationReconnect
	// AllocationDeliver replaces delivered credentials with current ones.
	AllocationDeliver
	// AllocationSyncInputs hands a Managed Computer the current inputs of its
	// Work; it is never part of a start.
	AllocationSyncInputs
)

// AllocationStatus names an outstanding allocation and its missing step.
type AllocationStatus struct {
	AllocationID string
	Step         AllocationStep
}

// ExecutionEnvironmentPort reaches the execution environments of the Works'
// runs: OpenSandbox sandboxes on a Cloud and the Work's directory on a Managed
// Computer. It carries out the environment steps of each run's allocation,
// identified by the allocation, and reports the step the allocation needs
// next; the environment authority of each Computer owns actual environment
// state.
type ExecutionEnvironmentPort interface {
	// ReconcileAllocation advances one allocation from its durable facts. It
	// calls the environment authority only before the first delivery and for
	// credential draining or removal.
	ReconcileAllocation(context.Context, string) (AllocationStep, error)
	// PrepareAllocation waits for the environment preparation to exit, bounded
	// by the caller, then starts the Harness and connects it.
	PrepareAllocation(context.Context, string) error
	// ReconnectAllocation resolves the sandbox again and connects its running
	// process; an absent sandbox is the loss of the Work's environment.
	ReconnectAllocation(context.Context, string) error
	// DeliverCredentials replaces a delivered allocation's credentials with
	// those Marketplace supplies now; one it cannot supply loses its binding.
	DeliverCredentials(context.Context, string) error
	// MarkCredentialsChanged durably marks the deliveries of the live
	// allocations whose Work uses the Provider Authorization as due, and lists
	// the delivered ones to wake.
	MarkCredentialsChanged(ctx context.Context, authorizationID string) ([]string, error)
	// SyncInputs hands a delivered Managed allocation's machine the current
	// inputs of its Work while they are due.
	SyncInputs(context.Context, string) error
	// MarkInputsChanged durably marks the inputs of the live Managed
	// allocations whose Work's input source is owner's File Space as due, and
	// lists the delivered ones to wake.
	MarkInputsChanged(ctx context.Context, owner string) ([]string, error)
	// ObserveSandbox checks an allocation against its sandboxes after the
	// environment authority reported a change: loss, endpoint change, surplus.
	ObserveSandbox(context.Context, string) (AllocationStep, error)
	// LiveAllocation returns the Work's one live allocation, if any.
	LiveAllocation(context.Context, string) (string, bool, error)
	// ListOutstandingAllocations pages allocations with unfinished obligations
	// and the step each is missing, judged from durable facts and Dispatcher's
	// stream ownership only; retired allocations whose obligations are
	// complete are not listed. An allocation it could not judge is listed as
	// settled alongside the error.
	ListOutstandingAllocations(context.Context, string, int) ([]AllocationStatus, error)
	// ReconcileComputer reconciles a Managed Computer that is reachable again:
	// it ends the processes of Works no longer running there and removes the
	// files of Works gone from it before anything new starts on it (Computer
	// model: Reconciliation). It does nothing where no Managed Computer is
	// reached.
	ReconcileComputer(ctx context.Context, computerID string) error
}

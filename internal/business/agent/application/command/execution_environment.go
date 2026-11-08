package command

import (
	"context"
	"errors"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ErrEnvironmentActionRefused means the AgentExecution no longer admits the
// launch or credential delivery an environment operation was taking: its Work
// asked its process to exit, or it no longer wants its run in the bound
// environment (Admit environment action).
var ErrEnvironmentActionRefused = errors.New("agent: the execution no longer admits the environment action")

// EnvironmentHolding asks, between the remote calls of an environment
// operation, whether the AgentExecution still admits the action the operation
// takes; it answers ErrEnvironmentActionRefused once it does not. The
// operation stops there, and clears what it delivered meanwhile.
type EnvironmentHolding func(ctx context.Context) error

// ExecutionEnvironmentPort reaches the execution environments of the Works'
// runs: OpenSandbox sandboxes on a Cloud and the Work's directory on a Managed
// Computer. It carries out the environment steps each AgentExecution decides
// and reports what it observed; it decides none of them, and the environment
// authority of each Computer owns actual environment state.
type ExecutionEnvironmentPort interface {
	// Observe shows the environments carrying the execution's ownership as
	// its authority lists them now, whether Dispatcher reads its launched
	// process there, and the model key a delivery would carry now.
	Observe(ctx context.Context, execution *domain.AgentExecution) (domain.EnvironmentObservation, error)
	// PrepareCreation fixes the environment's specification from the
	// execution and its Mount Grants and what its submission freezes. It is
	// repeatable and creates nothing.
	PrepareCreation(ctx context.Context, execution *domain.AgentExecution, mounts domain.SandboxMounts) error
	// SubmitCreation submits the environment's creation once and waits for
	// its answer, longer than the authority takes to create it. An error
	// means the answer is lost; a definite answer binds, or refuses after a
	// refusal or failure that left nothing created.
	SubmitCreation(ctx context.Context, execution *domain.AgentExecution) (domain.CreationAnswer, error)
	// DeleteEnvironment deletes one environment carrying the execution's
	// ownership, clearing its Vault first; a gone one is deleted already.
	DeleteEnvironment(ctx context.Context, execution *domain.AgentExecution, environmentID string) error
	// DeliverCredentials delivers the Work's current credentials, with what
	// Application supplied from other authorities, into the bound
	// environment, replacing earlier ones, and reports what it delivered. It
	// asks holding before each write, and clears what it wrote once holding
	// refuses.
	DeliverCredentials(ctx context.Context, execution *domain.AgentExecution, supply CredentialSupply, holding EnvironmentHolding) (domain.CredentialDelivery, error)
	// WithdrawCredentials clears the bound environment's credentials and
	// discards an unfinished preparation.
	WithdrawCredentials(ctx context.Context, execution *domain.AgentExecution) error
	// Prepare sees the in-environment preparation through to its exit,
	// bounded by the caller, and reports each selected Plugin's installation
	// and each remaining repository's checkout. It asks holding before it
	// starts the preparation process.
	Prepare(ctx context.Context, execution *domain.AgentExecution, holding EnvironmentHolding) (domain.PreparationReport, error)
	// Launch starts the Harness in the bound environment and returns its
	// process. It asks holding right before the start.
	Launch(ctx context.Context, execution *domain.AgentExecution, holding EnvironmentHolding) (string, error)
	// Connect hands the launched process to Dispatcher.
	Connect(ctx context.Context, execution *domain.AgentExecution) error
	// RenewLease renews the bound environment's lease to expiresAt and
	// reports the expiry the authority returned.
	RenewLease(ctx context.Context, execution *domain.AgentExecution, expiresAt time.Time) (time.Time, error)
	// SyncInputs hands a Managed Computer the current files of its Work's
	// located inputs and reports how many due changes it covered. It asks
	// holding before the sync, whose downloads authenticate with the
	// delivered credentials.
	SyncInputs(ctx context.Context, execution *domain.AgentExecution, inputs ManagedInputs, holding EnvironmentHolding) (int64, error)
	// ReconcileComputer reconciles a Managed Computer that is reachable again:
	// it ends the processes of Works no longer running there and removes the
	// files of Works gone from it before anything new starts on it (Computer
	// model: Reconciliation). It does nothing where no Managed Computer is
	// reached.
	ReconcileComputer(ctx context.Context, computerID string) error
}

// CredentialSupply is what a delivery hands the bound environment from
// other authorities, obtained by Application: the Space Token of the Work's
// publication target, with which its Agent publishes (Space Token in
// sandbox).
type CredentialSupply struct {
	SpaceToken string
}

// ManagedInputs are the current files of a Managed Work's located inputs,
// each with a fresh download Access Link; Located is false for a Work whose
// layout located no inputs, which has none to copy.
type ManagedInputs struct {
	Located bool
	Files   []InputFile
}

// InputFile is one file of a Work's located inputs and the path of its Access
// Link, as Storage issues it for the platform's public address.
type InputFile struct {
	Name       string
	SizeBytes  int64
	UpdatedAt  time.Time
	AccessPath string
}

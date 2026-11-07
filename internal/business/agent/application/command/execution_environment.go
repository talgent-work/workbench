package command

import (
	"context"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

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
	// DeliverCredentials delivers the Work's current credentials into the
	// bound environment, replacing earlier ones, and reports what it
	// delivered.
	DeliverCredentials(ctx context.Context, execution *domain.AgentExecution) (domain.CredentialDelivery, error)
	// WithdrawCredentials clears the bound environment's credentials and
	// discards an unfinished preparation.
	WithdrawCredentials(ctx context.Context, execution *domain.AgentExecution) error
	// Prepare sees the in-environment preparation through to its exit,
	// bounded by the caller, and reports each selected Plugin's installation
	// and each remaining repository's checkout.
	Prepare(ctx context.Context, execution *domain.AgentExecution) (domain.PreparationReport, error)
	// Launch starts the Harness in the bound environment and returns its
	// process.
	Launch(ctx context.Context, execution *domain.AgentExecution) (string, error)
	// Connect hands the launched process to Dispatcher.
	Connect(ctx context.Context, execution *domain.AgentExecution) error
	// RenewLease renews the bound environment's lease to expiresAt and
	// reports the expiry the authority returned.
	RenewLease(ctx context.Context, execution *domain.AgentExecution, expiresAt time.Time) (time.Time, error)
	// SyncInputs hands a Managed Computer the current inputs of its Work and
	// reports how many due changes it covered.
	SyncInputs(ctx context.Context, execution *domain.AgentExecution) (int64, error)
	// ReconcileComputer reconciles a Managed Computer that is reachable again:
	// it ends the processes of Works no longer running there and removes the
	// files of Works gone from it before anything new starts on it (Computer
	// model: Reconciliation). It does nothing where no Managed Computer is
	// reached.
	ReconcileComputer(ctx context.Context, computerID string) error
}

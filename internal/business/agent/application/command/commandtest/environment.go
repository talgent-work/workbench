package commandtest

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Environment stands in for the execution environment port: it answers a
// test's observation and creation answer, and records every step taken. Like
// the port's realizations, it asks an operation's holding check before each
// remote call and takes nothing it refuses; a delivery refused once written
// is cleared.
type Environment struct {
	mu sync.Mutex
	// Observation is what Observe shows; Observed lets it depend on the
	// execution.
	Observation domain.EnvironmentObservation
	Observed    func(execution *domain.AgentExecution) domain.EnvironmentObservation
	ObserveErr  error
	// Answer is SubmitCreation's answer; SubmitErr loses it. SubmitFunc runs
	// while the creation waits for its answer.
	Answer     domain.CreationAnswer
	SubmitErr  error
	SubmitFunc func(ctx context.Context)
	// PrepareFunc runs as Prepare; nil prepares at once.
	PrepareFunc func(ctx context.Context) error
	// Installations is what a preparation reports of its Plugins.
	Installations []domain.PluginInstallation
	// Checkouts is what a preparation reports of the repositories it checks
	// out.
	Checkouts []domain.RepositoryCheckout
	// ProcessID is the process Launch starts, "process" unless named.
	ProcessID string
	Delivery  domain.CredentialDelivery
	// DuringDelivery runs after a delivery wrote, before it asks its holding
	// check again.
	DuringDelivery func(ctx context.Context)
	StepErr        error

	Observations, Specified, Submitted int
	// SubmittedState is the execution each submission was sent for.
	SubmittedState                          []domain.AgentExecution
	Deleted                                 []string
	Delivered, Withdrawn, Prepared          int
	Launched, Connected, Synced, Reconciled int
	// Supplies are what each delivery was handed from other authorities,
	// and SyncedInputs the inputs each sync was handed.
	Supplies     []command.CredentialSupply
	SyncedInputs []command.ManagedInputs
	// Cleared counts deliveries cleared once their holding check refused.
	Cleared int
	// Renewals are the expiries each lease renewal asked for; each is
	// granted.
	Renewals []time.Time
}

var _ command.ExecutionEnvironmentPort = (*Environment)(nil)

func (e *Environment) Observe(_ context.Context, execution *domain.AgentExecution) (domain.EnvironmentObservation, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Observations++
	if e.ObserveErr != nil {
		return domain.EnvironmentObservation{}, e.ObserveErr
	}
	if e.Observed != nil {
		return e.Observed(execution), nil
	}
	return e.Observation, nil
}

func (e *Environment) PrepareCreation(context.Context, *domain.AgentExecution, domain.SandboxMounts) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Specified++
	return nil
}

func (e *Environment) SubmitCreation(ctx context.Context, execution *domain.AgentExecution) (domain.CreationAnswer, error) {
	e.mu.Lock()
	e.Submitted++
	e.SubmittedState = append(e.SubmittedState, *execution)
	wait := e.SubmitFunc
	e.mu.Unlock()
	if wait != nil {
		wait(ctx)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Answer, e.SubmitErr
}

func (e *Environment) DeleteEnvironment(_ context.Context, _ *domain.AgentExecution, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Deleted = append(e.Deleted, id)
	return e.StepErr
}

func (e *Environment) DeliverCredentials(ctx context.Context, _ *domain.AgentExecution, supply command.CredentialSupply, holding command.EnvironmentHolding) (domain.CredentialDelivery, error) {
	if err := holding(ctx); err != nil {
		return domain.CredentialDelivery{}, err
	}
	e.mu.Lock()
	during := e.DuringDelivery
	e.mu.Unlock()
	if during != nil {
		during(ctx)
	}
	if err := holding(ctx); err != nil {
		e.mu.Lock()
		e.Cleared++
		e.mu.Unlock()
		return domain.CredentialDelivery{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Delivered++
	e.Supplies = append(e.Supplies, supply)
	return e.Delivery, e.StepErr
}

func (e *Environment) WithdrawCredentials(context.Context, *domain.AgentExecution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Withdrawn++
	return e.StepErr
}

func (e *Environment) Prepare(ctx context.Context, _ *domain.AgentExecution, holding command.EnvironmentHolding) (domain.PreparationReport, error) {
	if err := holding(ctx); err != nil {
		return domain.PreparationReport{}, err
	}
	e.mu.Lock()
	e.Prepared++
	prepare := e.PrepareFunc
	report := domain.PreparationReport{Installations: slices.Clone(e.Installations), Checkouts: slices.Clone(e.Checkouts)}
	e.mu.Unlock()
	if prepare != nil {
		if err := prepare(ctx); err != nil {
			return domain.PreparationReport{}, err
		}
	}
	return report, nil
}

func (e *Environment) Launch(ctx context.Context, _ *domain.AgentExecution, holding command.EnvironmentHolding) (string, error) {
	if err := holding(ctx); err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Launched++
	if e.ProcessID == "" {
		return "process", e.StepErr
	}
	return e.ProcessID, e.StepErr
}

func (e *Environment) Connect(context.Context, *domain.AgentExecution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Connected++
	return e.StepErr
}

func (e *Environment) SyncInputs(ctx context.Context, execution *domain.AgentExecution, inputs command.ManagedInputs, holding command.EnvironmentHolding) (int64, error) {
	if err := holding(ctx); err != nil {
		return 0, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Synced++
	e.SyncedInputs = append(e.SyncedInputs, inputs)
	return execution.Inputs.Due, e.StepErr
}

func (e *Environment) ReconcileComputer(context.Context, string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Reconciled++
	return nil
}

func (e *Environment) RenewLease(_ context.Context, _ *domain.AgentExecution, expiresAt time.Time) (time.Time, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Renewals = append(e.Renewals, expiresAt)
	return expiresAt, e.StepErr
}

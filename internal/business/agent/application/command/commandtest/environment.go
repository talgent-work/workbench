package commandtest

import (
	"context"
	"slices"
	"sync"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Environment stands in for the execution environment port: it answers a
// test's observation and creation answer, and records every step taken.
type Environment struct {
	mu sync.Mutex
	// Observation is what Observe shows; Observed lets it depend on the
	// execution.
	Observation domain.EnvironmentObservation
	Observed    func(execution *domain.AgentExecution) domain.EnvironmentObservation
	ObserveErr  error
	// Answer is SubmitCreation's answer; SubmitErr loses it.
	Answer    domain.CreationAnswer
	SubmitErr error
	// PrepareFunc runs as Prepare; nil prepares at once.
	PrepareFunc func(ctx context.Context) error
	// Installations is what a preparation reports of its Plugins.
	Installations []domain.PluginInstallation
	// ProcessID is the process Launch starts, "process" unless named.
	ProcessID string
	Delivery  domain.CredentialDelivery
	StepErr   error

	Observations, Specified, Submitted int
	// SubmittedState is the execution each submission was sent for.
	SubmittedState                          []domain.AgentExecution
	Deleted                                 []string
	Delivered, Withdrawn, Prepared          int
	Launched, Connected, Synced, Reconciled int
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

func (e *Environment) SubmitCreation(_ context.Context, execution *domain.AgentExecution) (domain.CreationAnswer, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Submitted++
	e.SubmittedState = append(e.SubmittedState, *execution)
	return e.Answer, e.SubmitErr
}

func (e *Environment) DeleteEnvironment(_ context.Context, _ *domain.AgentExecution, id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Deleted = append(e.Deleted, id)
	return e.StepErr
}

func (e *Environment) DeliverCredentials(context.Context, *domain.AgentExecution) (domain.CredentialDelivery, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Delivered++
	return e.Delivery, e.StepErr
}

func (e *Environment) WithdrawCredentials(context.Context, *domain.AgentExecution) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Withdrawn++
	return e.StepErr
}

func (e *Environment) Prepare(ctx context.Context, _ *domain.AgentExecution) ([]domain.PluginInstallation, error) {
	e.mu.Lock()
	e.Prepared++
	prepare, installations := e.PrepareFunc, slices.Clone(e.Installations)
	e.mu.Unlock()
	if prepare != nil {
		if err := prepare(ctx); err != nil {
			return nil, err
		}
	}
	return installations, nil
}

func (e *Environment) Launch(context.Context, *domain.AgentExecution) (string, error) {
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

func (e *Environment) SyncInputs(_ context.Context, execution *domain.AgentExecution) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Synced++
	return execution.Inputs.Due, e.StepErr
}

func (e *Environment) ReconcileComputer(context.Context, string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.Reconciled++
	return nil
}

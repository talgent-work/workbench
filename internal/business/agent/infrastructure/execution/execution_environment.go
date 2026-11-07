package execution

import (
	"context"
	"errors"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// ExecutionEnvironment realizes the execution environment steps for every
// Computer alike: EnvironmentRecovery carries each allocation through its
// steps, and a Managed Computer that is reachable again is reconciled by the
// Managed realization, absent where no Managed Computer is reached.
type ExecutionEnvironment struct {
	*EnvironmentRecovery
	managed *ManagedReconciliation
}

var _ command.ExecutionEnvironmentPort = (*ExecutionEnvironment)(nil)

// NewExecutionEnvironment takes managed nil for a deployment that reaches no
// Managed Computer.
func NewExecutionEnvironment(recovery *EnvironmentRecovery, managed *ManagedEnvironment) (*ExecutionEnvironment, error) {
	if recovery == nil {
		return nil, errors.New("agent: environment recovery is required for execution environments")
	}
	environment := &ExecutionEnvironment{EnvironmentRecovery: recovery}
	if managed != nil {
		environment.managed = managed.Reconciliation()
	}
	return environment, nil
}

// ReconcileComputer reconciles a Managed Computer that is reachable again;
// without Managed Computers there is none to reconcile.
func (e *ExecutionEnvironment) ReconcileComputer(ctx context.Context, computerID string) error {
	if e.managed == nil {
		return nil
	}
	return e.managed.ReconcileComputer(ctx, computerID)
}

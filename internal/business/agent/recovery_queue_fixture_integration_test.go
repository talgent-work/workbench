//go:build integration

package agent_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	pkgtaskqueue "github.com/talgent-work/workbench/pkg/taskqueue"
	"go.uber.org/fx"
)

// recoveryQueues runs the production recovery task queues on the test Redis.
// Retries are fast so failure paths settle within a test's patience.
func recoveryQueues(t *testing.T) fx.Option {
	t.Helper()
	address := recoveryRedis(t)
	queue := func(name string, workers int) pkgtaskqueue.Config {
		return pkgtaskqueue.Config{RedisAddr: address, QueueName: name, WorkerConcurrency: workers, RetryDelayMin: pkgtaskqueue.Duration(50 * time.Millisecond), RetryDelayMax: pkgtaskqueue.Duration(500 * time.Millisecond), ShutdownTimeout: pkgtaskqueue.Duration(5 * time.Second)}
	}
	return fx.Options(
		// Sweep often, so an obligation no event wakes settles within a test.
		fx.Supply(agent.RecoveryConfig{SweepInterval: agent.Duration(time.Second)}),
		fx.Supply(
			fx.Annotated{Name: agent.RecoveryQueue, Target: queue("agent-recovery", 4)},
			fx.Annotated{Name: agent.PreparationQueue, Target: queue("agent-preparation", 2)},
		),
		pkgtaskqueue.NamedModule(agent.RecoveryQueue),
		pkgtaskqueue.NamedModule(agent.PreparationQueue),
	)
}

var recoveryRedisByTest sync.Map

// recoveryRedis holds the shared Redis once per test, which may compose the
// recovery module again to simulate a restart.
func recoveryRedis(t *testing.T) string {
	t.Helper()
	if address, ok := recoveryRedisByTest.Load(t); ok {
		return address.(string)
	}
	address := middlewaretest.Redis(t)
	recoveryRedisByTest.Store(t, address)
	t.Cleanup(func() { recoveryRedisByTest.Delete(t) })
	return address
}

// noEnvironments stands in for execution integration where a test controls
// execution through its own port and has no allocations.
type noEnvironments struct{}

var _ application.ExecutionEnvironmentRecovery = noEnvironments{}

func (noEnvironments) ReconcileAllocation(context.Context, string) (application.AllocationStep, error) {
	return application.AllocationSettled, nil
}
func (noEnvironments) PrepareAllocation(context.Context, string) error   { return nil }
func (noEnvironments) ReconnectAllocation(context.Context, string) error { return nil }
func (noEnvironments) ObserveSandbox(context.Context, string) (application.AllocationStep, error) {
	return application.AllocationSettled, nil
}
func (noEnvironments) DeliverCredentials(context.Context, string) error { return nil }
func (noEnvironments) SyncInputs(context.Context, string) error         { return nil }
func (noEnvironments) MarkCredentialsChanged(context.Context, string) ([]string, error) {
	return nil, nil
}
func (noEnvironments) MarkInputsChanged(context.Context, string) ([]string, error) {
	return nil, nil
}
func (noEnvironments) LiveAllocation(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (noEnvironments) ListOutstandingAllocations(context.Context, string, int) ([]application.AllocationStatus, error) {
	return nil, nil
}

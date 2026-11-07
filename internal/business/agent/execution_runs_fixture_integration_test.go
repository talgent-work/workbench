//go:build integration

package agent_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
)

// launchedProcess is the process every realized execution of these fixtures
// launches; feedback names it.
const launchedProcess = "process"

// realizedEnvironments stands in for execution integration: every
// environment an execution submits is created under the execution's own
// identity, which fits the recorded environment column, and runs at once, its Harness
// launches as launchedProcess, Dispatcher reads it, and a retired execution
// owns nothing once its environment's deletion completes. The executions' own
// reconciliation takes every step.
type realizedEnvironments struct {
	// pendingDeletion, when set and true, keeps a retired execution's
	// environment observed: its deletion has not completed.
	pendingDeletion *atomic.Bool
}

var _ command.ExecutionEnvironmentPort = realizedEnvironments{}

func (r realizedEnvironments) Observe(_ context.Context, e *domain.AgentExecution) (domain.EnvironmentObservation, error) {
	if e.Environment == "" || e.State == domain.ExecutionReleased {
		return domain.EnvironmentObservation{}, nil
	}
	if e.Retired() {
		if r.pendingDeletion != nil && r.pendingDeletion.Load() {
			return domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: e.Environment}}}, nil
		}
		return domain.EnvironmentObservation{}, nil
	}
	return domain.EnvironmentObservation{Environments: []domain.ObservedEnvironment{{ID: e.Environment, Running: true}}, Attached: e.Process.ID != ""}, nil
}
func (realizedEnvironments) PrepareCreation(context.Context, *domain.AgentExecution, domain.SandboxMounts) error {
	return nil
}
func (realizedEnvironments) SubmitCreation(_ context.Context, e *domain.AgentExecution) (domain.CreationAnswer, error) {
	return domain.CreationAnswer{EnvironmentID: e.ID}, nil
}
func (realizedEnvironments) DeleteEnvironment(context.Context, *domain.AgentExecution, string) error {
	return nil
}
func (realizedEnvironments) DeliverCredentials(context.Context, *domain.AgentExecution) (domain.CredentialDelivery, error) {
	return domain.CredentialDelivery{}, nil
}
func (realizedEnvironments) WithdrawCredentials(context.Context, *domain.AgentExecution) error {
	return nil
}
func (realizedEnvironments) Prepare(context.Context, *domain.AgentExecution) ([]domain.PluginInstallation, error) {
	return nil, nil
}
func (realizedEnvironments) Launch(context.Context, *domain.AgentExecution) (string, error) {
	return launchedProcess, nil
}
func (realizedEnvironments) Connect(context.Context, *domain.AgentExecution) error { return nil }
func (realizedEnvironments) SyncInputs(_ context.Context, e *domain.AgentExecution) (int64, error) {
	return e.Inputs.Due, nil
}
func (realizedEnvironments) ReconcileComputer(context.Context, string) error { return nil }

// moduleRuns drives the AgentExecutions of a composed Agent module where no
// execution integration does: it realizes each begun execution, reports its
// process's readiness and exit, and confirms its release, each saved through
// the module's repository and its reports applied to the Work at once.
type moduleRuns struct {
	mu           sync.Mutex
	executions   domain.AgentExecutionRepository
	coordination *command.ExecutionCoordination
}

// option captures the module's repository and coordination; a restarted
// module is captured again.
func (r *moduleRuns) option() fx.Option {
	return fx.Invoke(func(executions domain.AgentExecutionRepository, coordination *command.ExecutionCoordination) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.executions, r.coordination = executions, coordination
	})
}

func (r *moduleRuns) parts() (domain.AgentExecutionRepository, *command.ExecutionCoordination) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.executions, r.coordination
}

// latest is the Work's most recently begun execution, nil before its first.
func (r *moduleRuns) latest(t *testing.T, ctx context.Context, workID string) *domain.AgentExecution {
	t.Helper()
	executions, _ := r.parts()
	execution, err := executions.Latest(ctx, workID)
	if errors.Is(err, domain.ErrExecutionNotFound) {
		return nil
	}
	require.NoError(t, err)
	return execution
}

// await waits until the Work's latest execution satisfies want.
func (r *moduleRuns) await(t *testing.T, ctx context.Context, workID string, want func(*domain.AgentExecution) bool, message string) *domain.AgentExecution {
	t.Helper()
	var found *domain.AgentExecution
	require.Eventually(t, func() bool {
		found = r.latest(t, ctx, workID)
		return found != nil && want(found)
	}, 10*time.Second, 20*time.Millisecond, message)
	return found
}

// update applies a change to the Work's latest execution as it is now and
// saves it, again after a concurrent change.
func (r *moduleRuns) update(t *testing.T, ctx context.Context, workID string, change func(*domain.AgentExecution) bool) {
	t.Helper()
	executions, _ := r.parts()
	for range 20 {
		execution := r.latest(t, ctx, workID)
		require.NotNil(t, execution)
		if !change(execution) {
			return
		}
		err := executions.Save(ctx, execution)
		if !errors.Is(err, domain.ErrConcurrentModification) {
			require.NoError(t, err)
			return
		}
	}
	t.Fatalf("the execution of %s kept changing", workID)
}

// applyReports applies the Work's execution reports now rather than when
// their Domain Events are handled.
func (r *moduleRuns) applyReports(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	_, coordination := r.parts()
	require.NoError(t, coordination.ApplyExecutionReports(ctx, workID))
}

// realize creates the current start's environment and launches its process
// once its execution began and its Desk is seated.
func (r *moduleRuns) realize(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	r.await(t, ctx, workID, func(e *domain.AgentExecution) bool { return !e.Retired() && !e.Queued() }, "the admitted start's execution begins seated")
	r.update(t, ctx, workID, func(e *domain.AgentExecution) bool {
		if e.Process.ID != "" {
			return false
		}
		e.RecordSubmission()
		if e.Environment == "" {
			_, err := e.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: e.ID})
			require.NoError(t, err)
		}
		launched, err := e.RecordLaunch(launchedProcess)
		require.NoError(t, err)
		return launched
	})
}

// awaitLaunched waits until the current start's process was launched.
func (r *moduleRuns) awaitLaunched(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	r.await(t, ctx, workID, func(e *domain.AgentExecution) bool { return !e.Retired() && e.Process.ID == launchedProcess }, "the current start's process launches")
}

// ready reports both readiness reports of the launched process.
func (r *moduleRuns) ready(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	r.realize(t, ctx, workID)
	r.update(t, ctx, workID, func(e *domain.AgentExecution) bool {
		changed, err := e.ConfirmReady(domain.ReadinessReport{Prepared: true, Started: true}, time.Now())
		require.NoError(t, err)
		return changed
	})
	r.applyReports(t, ctx, workID)
}

// awaitExitRequest waits until the Work asked its execution's process to
// exit under stopID.
func (r *moduleRuns) awaitExitRequest(t *testing.T, ctx context.Context, workID, stopID string) {
	t.Helper()
	r.await(t, ctx, workID, func(e *domain.AgentExecution) bool { return e.ExitRequest.StopRequestID == stopID }, "the Work asks its process to exit")
}

// awaitRetired waits until the Work retired its execution.
func (r *moduleRuns) awaitRetired(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	r.await(t, ctx, workID, (*domain.AgentExecution).Retired, "the Work retires its execution")
}

// release confirms the release of the Work's retired execution, as its
// reconciliation does once its Desk left and it owns no environment.
func (r *moduleRuns) release(t *testing.T, ctx context.Context, workID string) {
	t.Helper()
	r.awaitRetired(t, ctx, workID)
	r.update(t, ctx, workID, func(e *domain.AgentExecution) bool {
		e.RecordDeskLeft(time.Now())
		released, err := e.ConfirmRelease(domain.EnvironmentObservation{}, time.Now())
		require.NoError(t, err)
		return released
	})
	r.applyReports(t, ctx, workID)
}

// launches are the LaunchSpecifications of every execution begun, in order.
func launches(t *testing.T, db *xorm.Engine) []domain.LaunchSpecification {
	t.Helper()
	var ids []string
	require.NoError(t, db.Table("execution_allocation").Cols("id").OrderBy("created_at, id").Find(&ids))
	repository := execution.NewAgentExecutionRepository(db, time.Now)
	result := make([]domain.LaunchSpecification, 0, len(ids))
	for _, id := range ids {
		begun, err := repository.Get(context.Background(), id)
		require.NoError(t, err)
		result = append(result, begun.Launch)
	}
	return result
}

// awaitLaunches waits until exactly count executions were begun.
func awaitLaunches(t *testing.T, db *xorm.Engine, count int, message string) []domain.LaunchSpecification {
	t.Helper()
	require.Eventually(t, func() bool { return len(launches(t, db)) == count }, 10*time.Second, 20*time.Millisecond, message)
	return launches(t, db)
}

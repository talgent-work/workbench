//go:build integration

package agent_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

// failingOnceEnvironments fails the first listing of outstanding allocations,
// lists one allocation afterwards and records its reconciliation.
type failingOnceEnvironments struct {
	noEnvironments
	mu         sync.Mutex
	listed     int
	reconciled int
}

func (e *failingOnceEnvironments) ListOutstandingAllocations(_ context.Context, after string, _ int) ([]application.AllocationStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listed++
	if e.listed == 1 {
		return nil, errors.New("mysql unavailable")
	}
	if after != "" {
		return nil, nil
	}
	return []application.AllocationStatus{{AllocationID: "allocation-1", Step: application.AllocationReconcile}}, nil
}

func (e *failingOnceEnvironments) ReconcileAllocation(context.Context, string) (application.AllocationStep, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.reconciled++
	return application.AllocationSettled, nil
}

func (e *failingOnceEnvironments) reconciledCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reconciled
}

// A failed sweep is not retried, and it must not suppress the sweeps after it:
// the production sweep schedule and uniqueness on the real queue.
func TestFailedSweepDoesNotSuppressLaterSweeps(t *testing.T) {
	environments := &failingOnceEnvironments{}
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	port := &sessionCommandRecorder{}
	app := fx.New(agent.Module, moduleCatalog(), sessionStateTransport(t), agent.RecoveryModule, recoveryQueues(t), fx.NopLogger,
		fx.Provide(func() application.ExecutionEnvironmentRecovery { return environments }),
		storageStub(),
		fx.Supply(http.NewServeMux(), slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(func() kubernetes.Interface { return fake.NewClientset() }),
		fx.Provide(func() domain.ExecutionPort { return port }),
		fx.Provide(func() command.InputBufferPort { return port }),
	)
	require.NoError(t, app.Err())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() {
		stop, release := context.WithTimeout(context.Background(), 10*time.Second)
		defer release()
		require.NoError(t, app.Stop(stop))
		require.NoError(t, db.Close())
	})
	require.Eventually(t, func() bool { return environments.reconciledCount() > 0 }, 10*time.Second, 20*time.Millisecond, "a later sweep wakes what the failed one missed")
}

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
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"go.uber.org/fx"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

// failingOnceLookups fails the first listing of outstanding executions and
// lists one execution afterwards.
type failingOnceLookups struct {
	agentquery.ExecutionRecoveryReader
	mu     sync.Mutex
	listed int
}

func (l *failingOnceLookups) ListOutstandingExecutions(_ context.Context, after string, _ int) ([]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.listed++
	if l.listed == 1 {
		return nil, errors.New("mysql unavailable")
	}
	if after != "" {
		return nil, nil
	}
	return []string{"execution-1"}, nil
}

// reconciledExecutions records each read of the listed execution, which its
// reconciliation makes first.
type reconciledExecutions struct {
	domain.AgentExecutionRepository
	mu         sync.Mutex
	reconciled int
}

func (r *reconciledExecutions) Get(ctx context.Context, id string) (*domain.AgentExecution, error) {
	if id == "execution-1" {
		r.mu.Lock()
		r.reconciled++
		r.mu.Unlock()
	}
	return r.AgentExecutionRepository.Get(ctx, id)
}

func (r *reconciledExecutions) reconciledCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconciled
}

// A failed sweep is not retried, and it must not suppress the sweeps after it:
// the production sweep schedule and uniqueness on the real queue.
func TestFailedSweepDoesNotSuppressLaterSweeps(t *testing.T) {
	lookups := &failingOnceLookups{}
	executions := &reconciledExecutions{}
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	port := &sessionCommandRecorder{}
	app := fx.New(agent.Module, moduleCatalog(), sessionStateTransport(t), agent.RecoveryModule, recoveryQueues(t), fx.NopLogger,
		fx.Provide(func() command.ExecutionEnvironmentPort { return realizedEnvironments{} }),
		fx.Supply(domain.LeaseTerms{}),
		fx.Decorate(func(agentquery.ExecutionRecoveryReader) agentquery.ExecutionRecoveryReader { return lookups }),
		fx.Decorate(func(repository domain.AgentExecutionRepository) domain.AgentExecutionRepository {
			executions.AgentExecutionRepository = repository
			return executions
		}),
		storageStub(),
		fx.Supply(http.NewServeMux(), slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(func() kubernetes.Interface { return fake.NewClientset() }),
		fx.Provide(func() command.RunningHarnessPort { return port }),
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
	require.Eventually(t, func() bool { return executions.reconciledCount() > 0 }, 10*time.Second, 20*time.Millisecond, "a later sweep wakes what the failed one missed")
}

package connectrpc_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
)

type wakeRecorder struct {
	mu   sync.Mutex
	keys []string
}

func (r *wakeRecorder) Enqueue(_ context.Context, task taskqueue.Task, _ ...taskqueue.EnqueueOption) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, task.ID())
	return nil
}

// liveExecution is an executions repository holding one unretired execution
// of the Work.
func liveExecution(t *testing.T, workID string) (*rpcExecutions, string) {
	t.Helper()
	executions := &rpcExecutions{now: time.Now}
	execution, err := domain.BeginExecution(domain.ExecutionBeginning{
		Admitted:    domain.WorkStartAdmitted{WorkID: workID, StartAdmittedAt: time.Now(), DeskID: "desk-1"},
		Launch:      domain.LaunchSpecification{WorkID: workID, HomeBinding: domain.HomeBinding{OwnerID: "owner", Namespace: "home"}},
		Realization: domain.RealizationCloud,
	})
	require.NoError(t, err)
	require.NoError(t, executions.Save(context.Background(), execution))
	return executions, execution.ID
}

// Dispatcher's report of an ended connection only reconnects the Work's
// unretired execution; it changes no Work state and needs the integration
// credential.
func TestConnectionEndedFromDispatcherReconnectsTheLiveExecution(t *testing.T) {
	ctx := context.Background()
	wakes := &wakeRecorder{}
	executions, executionID := liveExecution(t, "work-1")
	recovery := application.NewRecovery(&application.Application{}, wakes, executions, nil, nil, nil, nil)
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, transport.NewFeedbackHandler(&application.Application{}, recovery, "integration-secret"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	require.NoError(t, agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(ctx, "work-1", "process-1"))
	require.Equal(t, []string{"agent:allocation-reconnect:" + executionID}, wakes.keys)
	require.NoError(t, agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(ctx, "stopped-work", "process-1"), "a Work without an execution has nothing to reconnect")
	require.Len(t, wakes.keys, 1)

	unauthenticated := agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	_, err := unauthenticated.ConnectionEnded(ctx, &agentprivatev1.ConnectionEndedRequest{WorkId: "work-1"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Len(t, wakes.keys, 1)
}

func TestConnectionEndedIsRefusedWhereNoRecoveryRuns(t *testing.T) {
	handler := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, transport.NewFeedbackHandler(&application.Application{}, nil, "integration-secret"))
	})
	server := httptest.NewServer(handler)
	defer server.Close()
	err := agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(context.Background(), "work-1", "process-1")
	require.Error(t, err)
}

package connectrpc_test

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
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

type liveAllocations map[string]string

func (liveAllocations) ReconcileAllocation(context.Context, string) (application.AllocationStep, error) {
	return application.AllocationSettled, nil
}
func (liveAllocations) PrepareAllocation(context.Context, string) error   { return nil }
func (liveAllocations) ReconnectAllocation(context.Context, string) error { return nil }
func (liveAllocations) ObserveSandbox(context.Context, string) (application.AllocationStep, error) {
	return application.AllocationSettled, nil
}
func (liveAllocations) DeliverCredentials(context.Context, string) error { return nil }
func (liveAllocations) SyncInputs(context.Context, string) error         { return nil }
func (liveAllocations) MarkCredentialsChanged(context.Context, string) ([]string, error) {
	return nil, nil
}
func (liveAllocations) MarkInputsChanged(context.Context, string) ([]string, error) {
	return nil, nil
}
func (l liveAllocations) LiveAllocation(_ context.Context, workID string) (string, bool, error) {
	id, ok := l[workID]
	return id, ok, nil
}
func (liveAllocations) ListOutstandingAllocations(context.Context, string, int) ([]application.AllocationStatus, error) {
	return nil, nil
}

// Dispatcher's report of an ended connection only reconnects the Work's live
// allocation; it changes no Work state and needs the integration credential.
func TestConnectionEndedFromDispatcherReconnectsTheLiveAllocation(t *testing.T) {
	ctx := context.Background()
	wakes := &wakeRecorder{}
	recovery := application.NewRecovery(&application.Application{}, wakes, liveAllocations{"work-1": "allocation-1"}, nil, nil)
	_, handler := agentprivatev1connect.NewExecutionFeedbackServiceHandler(transport.NewFeedbackHandler(&application.Application{}, recovery, "integration-secret"))
	server := httptest.NewServer(handler)
	defer server.Close()

	require.NoError(t, agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(ctx, "work-1", "process-1"))
	require.Equal(t, []string{"agent:allocation-reconnect:allocation-1"}, wakes.keys)
	require.NoError(t, agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(ctx, "stopped-work", "process-1"), "a Work without a live allocation has nothing to reconnect")
	require.Len(t, wakes.keys, 1)

	unauthenticated := agentprivatev1connect.NewExecutionFeedbackServiceClient(server.Client(), server.URL)
	_, err := unauthenticated.ConnectionEnded(ctx, connect.NewRequest(&agentprivatev1.ConnectionEndedRequest{WorkId: "work-1"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Len(t, wakes.keys, 1)
}

func TestConnectionEndedIsRefusedWhereNoRecoveryRuns(t *testing.T) {
	_, handler := agentprivatev1connect.NewExecutionFeedbackServiceHandler(transport.NewFeedbackHandler(&application.Application{}, nil, "integration-secret"))
	server := httptest.NewServer(handler)
	defer server.Close()
	err := agentfeedback.NewClient(server.Client(), server.URL, "integration-secret").ReportConnectionEnded(context.Background(), "work-1", "process-1")
	require.Error(t, err)
}

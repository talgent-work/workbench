//go:build integration

package dispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// The sandbox side of a connection to a process that has not exited fails:
// Dispatcher reports it to Agent only after giving the stream up, so Agent's
// next Connect starts a new one instead of being absorbed by the ended one.
func TestEndedConnectionIsReportedAfterItIsForgotten(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "process stream unavailable", http.StatusBadGateway)
	}))
	defer unreachable.Close()
	records := &nativeRecords{}
	manager := newNativeReplicas(t).replica("replica", records, newTestDelivery(t, records, records))
	target := command.ConnectExecution{WorkID: uuid.NewString(), ProcessID: "process-1", NativeSessionID: uuid.NewString(), Endpoint: unreachable.URL, AccessToken: "execd"}

	require.NoError(t, manager.Connect(ctx, target))
	require.Eventually(t, func() bool { return len(records.connectionsEnded()) == 1 }, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{target.WorkID + "/" + target.ProcessID}, records.connectionsEnded())

	require.NoError(t, manager.Connect(ctx, target), "the ended connection no longer absorbs a new Connect")
	require.Eventually(t, func() bool { return len(records.connectionsEnded()) == 2 }, 10*time.Second, 20*time.Millisecond)
}

// Shutdown is not an ended connection: the replica hands the stream to a peer
// without Agent.
func TestShutdownDoesNotReportConnectionEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	attached := make(chan struct{}, 1)
	holding := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case attached <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer holding.Close()
	records := &nativeRecords{}
	manager := newNativeReplicas(t).replica("replica", records, newTestDelivery(t, records, records))
	require.NoError(t, manager.Connect(ctx, command.ConnectExecution{WorkID: uuid.NewString(), ProcessID: "process-1", NativeSessionID: uuid.NewString(), Endpoint: holding.URL, AccessToken: "execd"}))
	select {
	case <-attached:
	case <-ctx.Done():
		t.Fatal("the connection never attached")
	}
	stop, done := context.WithTimeout(context.Background(), 5*time.Second)
	defer done()
	require.NoError(t, manager.Shutdown(stop))
	require.Empty(t, records.connectionsEnded())
}

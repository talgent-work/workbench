//go:build integration

package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

// preparingAllocation is a delivered allocation whose preparation exits once
// attached, with a Dispatcher recording Connects.
type preparingAllocation struct {
	connections *NativeConnections
	receiver    *nativeConnectionReceiver
	db          *xorm.Engine
	record      allocationRecord
	exited      chan time.Time
}

func newPreparingAllocation(t *testing.T) *preparingAllocation {
	t.Helper()
	ctx := context.Background()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	attachNativeCredentials(t, db, provisioning, lifecycle)
	input := request()
	input.Capabilities, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}}, nil)
	require.NoError(t, err)
	require.NoError(t, provisioning.Register(ctx, input, testMounts))
	require.NoError(t, submitPending(ctx, provisioning))
	var created atomic.Int32
	exited := make(chan time.Time, 1)
	processAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pty/preparation-handle/ws" {
			websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"preparation-handle","mode":"pipe","role":"holder"}`)
				exited <- time.Now()
				_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
			}).ServeHTTP(w, r)
			return
		}
		if created.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"session_id":"preparation-handle"}`))
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"harness-handle"}`))
	}))
	t.Cleanup(processAPI.Close)
	lifecycle.endpoint = processAPI.URL
	receiver := &nativeConnectionReceiver{provisioning: provisioning}
	mux := http.NewServeMux()
	path, handler := "/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, receiver) })
	mux.Handle(path, handler)
	dispatcher := httptest.NewServer(mux)
	t.Cleanup(dispatcher.Close)
	connections, err := NewNativeConnections(provisioning, provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret", &reclamationFeedback{}, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	record, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	ready, err := connections.deliver(ctx, record)
	require.NoError(t, err)
	require.True(t, ready, "the first delivery precedes preparation")
	record, _, err = latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	return &preparingAllocation{connections: connections, receiver: receiver, db: db, record: record, exited: exited}
}

// The preparation's exit starts the Harness and connects it in the same
// attempt, without waiting for another wake.
func TestPreparationExitStartsAndConnectsTheHarnessAtOnce(t *testing.T) {
	ctx := context.Background()
	f := newPreparingAllocation(t)
	require.NoError(t, f.connections.prepare(ctx, f.record))
	exit := <-f.exited
	connected, _, err := latestAllocation(ctx, f.db, f.record.SessionID)
	require.NoError(t, err)
	require.True(t, connected.EnvironmentPrepared)
	require.Equal(t, "harness-handle", connected.ProcessID)
	require.Len(t, f.receiver.requests, 1)
	require.Equal(t, "harness-handle", f.receiver.requests[0].GetProcessId())
	require.Less(t, time.Since(exit), 5*time.Second, "the Harness connects as soon as preparation exits")
}

// A stop admitted while preparation ran keeps the Harness from starting.
func TestStopAdmittedDuringPreparationStartsNoHarness(t *testing.T) {
	ctx := context.Background()
	f := newPreparingAllocation(t)
	f.connections.credentials.works.sessions = stoppingSession{}
	require.ErrorIs(t, f.connections.prepare(ctx, f.record), errAllocationNotHeld)
	<-f.exited
	stopped, _, err := latestAllocation(ctx, f.db, f.record.SessionID)
	require.NoError(t, err)
	require.Empty(t, stopped.ProcessID)
	require.Empty(t, f.receiver.requests)
}

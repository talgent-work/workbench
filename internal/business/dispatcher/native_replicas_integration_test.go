//go:build integration

package dispatcher_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	transport "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/transport/routing"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// fastTiming keeps takeover within a test's patience: liveness expires after
// a second and survivors scan every 200ms.
var fastTiming = dispatcherredis.OwnershipConfig{
	TTL: dispatcherredis.Duration(time.Second), Heartbeat: dispatcherredis.Duration(200 * time.Millisecond),
	Scan: dispatcherredis.Duration(200 * time.Millisecond), SnapshotTTL: dispatcherredis.Duration(time.Hour),
}

// nativeReplicas is the Redis shared by Dispatcher replicas in one test.
type nativeReplicas struct {
	t       *testing.T
	redis   string
	redisDB int
}

func newNativeReplicas(t *testing.T) *nativeReplicas {
	t.Helper()
	addr, db := middlewaretest.RedisDB(t)
	return &nativeReplicas{t: t, redis: addr, redisDB: db}
}

func (r *nativeReplicas) client() *goredis.Client {
	rdb := goredis.NewClient(&goredis.Options{Addr: r.redis, DB: r.redisDB})
	r.t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

// inputs is the Work input buffers the replicas share.
func (r *nativeReplicas) inputs() *dispatcherredis.InputBuffer {
	return dispatcherredis.NewInputBuffer(r.client())
}

func (r *nativeReplicas) ownership(instance, addr string) *dispatcherredis.Ownership {
	return dispatcherredis.NewOwnership(r.client(), instance, addr, fastTiming.TTL.Duration(), fastTiming.SnapshotTTL.Duration())
}

// replica starts a Dispatcher replica; it hands its streams over when the
// test ends unless the test already stopped it.
func (r *nativeReplicas) replica(instance string, feedback command.ExecutionFeedbackReporter, delivery *messagedelivery.Sink) *dispatcher.NativeExecutions {
	r.t.Helper()
	return r.replicaAt(instance, "127.0.0.1:0", feedback, delivery)
}

func (r *nativeReplicas) replicaAt(instance, addr string, feedback command.ExecutionFeedbackReporter, delivery *messagedelivery.Sink) *dispatcher.NativeExecutions {
	r.t.Helper()
	manager, err := dispatcher.NewNativeExecutions(http.DefaultClient, feedback, delivery, r.ownership(instance, addr), r.inputs(), fastTiming, slog.Default())
	require.NoError(r.t, err)
	require.NoError(r.t, manager.Start(context.Background()))
	r.t.Cleanup(func() {
		c, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = manager.Shutdown(c)
	})
	return manager
}

// seed stores a snapshot for a process, as a replica would at a boundary.
func (r *nativeReplicas) seed(target command.ConnectExecution, state *nativeprocess.ProtocolState) {
	r.t.Helper()
	ctx := context.Background()
	seeder := r.ownership("seeder", "127.0.0.1:0")
	gen, _, err := seeder.Claim(ctx, target.WorkID, &target)
	require.NoError(r.t, err)
	raw, err := nativeprocess.EncodeSnapshot(target, state)
	require.NoError(r.t, err)
	require.NoError(r.t, seeder.SaveSnapshot(ctx, target.WorkID, gen, raw))
	require.NoError(r.t, seeder.Abandon(ctx, target.WorkID, gen))
}

// stored reads a Work's latest snapshot for its process.
func (r *nativeReplicas) stored(target command.ConnectExecution) *nativeprocess.ProtocolState {
	r.t.Helper()
	raw, _, err := r.ownership("reader", "127.0.0.1:0").Snapshot(context.Background(), target.WorkID)
	require.NoError(r.t, err)
	state, err := nativeprocess.RestoreState(target, raw)
	require.NoError(r.t, err)
	return state
}

// servedReplica starts a replica whose private API is served at the address it
// registers, so peers can forward commands to it.
func (r *nativeReplicas) servedReplica(instance string, feedback command.ExecutionFeedbackReporter, delivery *messagedelivery.Sink) (*dispatcher.NativeExecutions, dispatcherprivatev1connect.ExecutionServiceClient) {
	r.t.Helper()
	server := httptest.NewUnstartedServer(nil)
	addr := server.Listener.Addr().String()
	manager := r.replicaAt(instance, addr, feedback, delivery)
	mux := routedMux(manager, r.ownership(instance, addr), r.inputs())
	server.Config.Handler = mux
	server.Start()
	r.t.Cleanup(server.Close)
	return manager, dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
}

func routedMux(manager *dispatcher.NativeExecutions, ownership *dispatcherredis.Ownership, buffer *dispatcherredis.InputBuffer) *http.ServeMux {
	app := application.NewApplication(manager, command.NewConnectExecutionHandler(manager), command.NewEnqueueInputHandler(buffer), command.NewDropInputHandler(buffer), command.NewStopExecutionHandler(manager), manager, manager, manager, manager, buffer)
	local := transport.NewExecutionHandler(app, "control-test")
	routed := routing.NewNativeExecutionHandler(local, ownership, ownership.Self(), "control-test", peer.NewNativeClients(http.DefaultClient))
	path, handler := "/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, routed) })
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	return mux
}

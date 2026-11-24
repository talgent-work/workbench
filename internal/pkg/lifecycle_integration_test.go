//go:build integration

package pkg_test

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	componenttaskqueue "github.com/go-jimu/components/taskqueue"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/talgent-work/workbench/internal/pkg"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/taskqueue"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

const (
	lane          = "lifecycle"
	echoProcedure = "/test.v1.EchoService/Echo"
	startupTask   = componenttaskqueue.TaskType("lifecycle.startup.v1")
	drainingTask  = componenttaskqueue.TaskType("lifecycle.draining.v1")
)

var echoSpec = connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: echoProcedure}

// Contexts composed in one process call each other's procedures over
// loopback, so an entry point's work reaches the process's own listener both
// as the process starts and while it drains: a task waiting when the lane
// starts, and a task that calls only once readiness is withdrawn.
func TestEntryPointsReachTheOwnListenerFromStartUntilTheyDrain(t *testing.T) {
	addr, db := middlewaretest.RedisDB(t)
	option := taskqueue.Option{RedisAddr: addr, RedisDB: db, QueueName: lane, WorkerConcurrency: 2, ShutdownTimeout: taskqueue.Duration(10 * time.Second)}.
		WithTaskCheckInterval(10 * time.Millisecond)
	var server connectrpc.Server
	calls := make(chan error, 2)
	running, draining := make(chan struct{}), make(chan struct{})
	processor := func(kind componenttaskqueue.TaskType, before func(context.Context)) func() componenttaskqueue.Processor {
		return func() componenttaskqueue.Processor {
			return componenttaskqueue.NewProcessor(kind, func(ctx context.Context, _ componenttaskqueue.Task) error {
				before(ctx)
				calls <- connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+server.Address())).
					CallUnary(context.Background(), echoSpec, &emptypb.Empty{}, &emptypb.Empty{})
				return nil
			})
		}
	}
	enqueue := func(kind componenttaskqueue.TaskType) {
		task, err := componenttaskqueue.New(componenttaskqueue.Definition{Type: kind, Queue: lane}, []byte("{}"))
		require.NoError(t, err)
		require.NoError(t, taskqueue.NewEnqueuer(option, slog.New(slog.DiscardHandler)).Enqueue(t.Context(), task))
	}
	// Waiting before the process starts, so the worker takes it at once.
	enqueue(startupTask)

	app := fxtest.New(t,
		fx.NopLogger,
		fx.Supply(slog.New(slog.DiscardHandler), connectrpc.Option{Addr: "127.0.0.1:0"}),
		fx.Supply(fx.Annotate(option, fx.ResultTags(`name:"`+lane+`"`))),
		connectrpc.Module,
		taskqueue.Module(lane),
		fx.Module("business.test",
			fx.Provide(
				fx.Annotate(processor(startupTask, func(context.Context) {}), fx.ResultTags(taskqueue.ProcessorTag(lane))),
				fx.Annotate(processor(drainingTask, func(context.Context) {
					close(running)
					<-draining
					for readiness(t, server.Address()) == grpchealth.StatusServing {
						time.Sleep(10 * time.Millisecond)
					}
				}), fx.ResultTags(taskqueue.ProcessorTag(lane))),
			),
			fx.Invoke(func(s connectrpc.Server) {
				server = s
				s.Register(func(rpc *connect.Server) {
					rpc.Register(connect.Method{Spec: echoSpec, Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
						if err := stream.Receive(&emptypb.Empty{}); err != nil {
							return err
						}
						return stream.Send(&emptypb.Empty{})
					}})
				})
			}),
		),
		pkg.Lifecycle,
	)
	app.RequireStart()
	select {
	case err := <-calls:
		require.NoError(t, err, "a task taken as the lane starts reaches the listener")
	case <-time.After(10 * time.Second):
		t.Fatal("the lane did not take the waiting task")
	}
	require.Equal(t, grpchealth.StatusServing, readiness(t, server.Address()))

	// Taken before the stop begins: a lane takes no new task once stopping.
	enqueue(drainingTask)
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		t.Fatal("the lane did not take the second task")
	}
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		close(draining)
		app.RequireStop()
	}()
	require.NoError(t, <-calls, "a task still running after readiness is withdrawn reaches the listener")
	<-stopped
}

func readiness(t *testing.T, address string) grpchealth.Status {
	t.Helper()
	client := grpchealth.NewClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+address, connecthttp.WithGRPC())))
	response, err := client.Check(context.Background(), &grpchealth.CheckRequest{})
	if err != nil {
		return grpchealth.StatusUnknown
	}
	return response.Status
}

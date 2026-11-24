package connectrpc_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
)

const echoProcedure = "/test.v1.EchoService/Echo"

var echoSpec = connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: echoProcedure}

// echo calls the echo procedure on the listener at address.
func echo(client *http.Client, address string, options ...connecthttp.Option) error {
	return connect.NewClient(connecthttp.NewTransport(client, "http://"+address, options...)).
		CallUnary(context.Background(), echoSpec, &emptypb.Empty{}, &emptypb.Empty{})
}

// Every procedure registered through the Server is reachable on its one
// listener, over HTTP/1.1 and unencrypted HTTP/2 alike.
func TestServerServesRegisteredProcedures(t *testing.T) {
	server, _ := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0"}, func(context.Context) error { return nil })

	for _, client := range []*http.Client{http.DefaultClient, connectrpc.NewH2CClient()} {
		err := echo(client, server.Address(), connecthttp.WithGRPC())
		require.NoError(t, err)
	}
}

// Readiness turns NOT_SERVING the moment shutdown begins and stays so for the
// shutdown delay, while liveness keeps reporting SERVING; health checks never
// reach the execution log.
func TestServerReportsReadinessUntilShutdown(t *testing.T) {
	server, logs := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0", ShutdownDelay: 300 * time.Millisecond}, func(context.Context) error { return nil })
	require.Equal(t, grpchealth.StatusServing, check(t, server.Address(), ""))
	require.Equal(t, grpchealth.StatusServing, check(t, server.Address(), connectrpc.LivenessService))

	stopped := make(chan error, 1)
	go func() { stopped <- server.Stop(context.Background()) }()
	require.Eventually(t, func() bool { return check(t, server.Address(), "") == grpchealth.StatusNotServing }, time.Second, 10*time.Millisecond)
	require.Equal(t, grpchealth.StatusServing, check(t, server.Address(), connectrpc.LivenessService))
	require.NoError(t, <-stopped)
	require.NotContains(t, logs.String(), "grpc.health.v1.Health")
}

// An address that cannot be bound fails startup instead of a later goroutine.
func TestServerStartFailsWhenTheAddressIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	server := connectrpc.New(connectrpc.Option{Addr: taken.Addr().String()}, discardLogger(), noShutdown{})
	require.Error(t, server.Start(context.Background()))
}

// Stop lets a running request finish before the listener closes.
func TestServerStopDrainsRunningRequests(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server, _ := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0"}, func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	result := make(chan error, 1)
	go func() { result <- echo(http.DefaultClient, server.Address()) }()
	<-started
	stopped := make(chan error, 1)
	go func() { stopped <- server.Stop(context.Background()) }()
	time.Sleep(50 * time.Millisecond)
	close(release)
	require.NoError(t, <-result)
	require.NoError(t, <-stopped)
}

// Each RPC is logged once as "request complete" at the level its outcome
// means for the service, with the cause behind the error the client sees.
func TestServerLogsOneEntryPerRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		code    string
		level   string
		outcome string
	}{
		{name: "success", level: "INFO", outcome: "success", code: "ok"},
		{name: "caller", err: connect.NewError(connect.CodeNotFound, "no such work"), level: "INFO", outcome: "failure", code: "not_found"},
		{name: "transient", err: connect.NewError(connect.CodeUnavailable, "agent down"), level: "WARN", outcome: "failure", code: "unavailable"},
		{name: "fault", err: errors.New("disk on fire"), level: "ERROR", outcome: "failure", code: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, logs := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0"}, func(context.Context) error { return tc.err })
			_ = echo(http.DefaultClient, server.Address())

			entries := completions(t, logs)
			require.Len(t, entries, 1)
			entry := entries[0]
			require.Equal(t, echoProcedure, entry["operation"])
			require.Equal(t, tc.level, entry["level"])
			require.Equal(t, tc.outcome, entry["outcome"])
			require.Equal(t, tc.code, entry["connect_code"])
			require.Contains(t, entry, "elapsed_ms")
			if tc.err != nil {
				require.Contains(t, logs.String(), strings.TrimPrefix(tc.err.Error(), connect.CodeOf(tc.err).String()+": "))
			}
		})
	}
}

// A panicking handler answers internal and is logged as a server fault; the
// process keeps serving.
func TestServerRecoversPanickingHandlers(t *testing.T) {
	server, logs := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0"}, func(context.Context) error { panic("boom") })
	err := echo(http.DefaultClient, server.Address())
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	entries := completions(t, logs)
	require.Len(t, entries, 1)
	require.Equal(t, "ERROR", entries[0]["level"])
	require.Equal(t, "internal", entries[0]["connect_code"])
}

// A context's interceptors, such as its authentication, run inside the
// shared chain for its own procedures only: the execution log still records
// and recovers every call, and another context's procedures never reach them.
func TestServerRunsEachContextsInterceptorsOnItsOwnProcedures(t *testing.T) {
	logs := &syncBuffer{}
	server := connectrpc.New(connectrpc.Option{Addr: "127.0.0.1:0"}, slog.New(slog.NewJSONHandler(logs, nil)), noShutdown{})
	var seen sync.Map
	refuse := func(name string) connect.ServerInterceptor {
		return func(next connect.ServerFunc) connect.ServerFunc {
			return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
				seen.Store(name+" "+spec.Procedure, true)
				if name == "refusing" {
					return connect.NewError(connect.CodeUnauthenticated, "no identity")
				}
				return next(ctx, spec, stream)
			}
		}
	}
	const otherProcedure = "/test.v1.OtherService/Echo"
	server.Register(func(s *connect.Server) { s.Register(echoMethod(echoProcedure)) }, connectrpc.WithInterceptors(refuse("refusing")))
	server.Register(func(s *connect.Server) { s.Register(echoMethod(otherProcedure)) }, connectrpc.WithInterceptors(refuse("admitting")))
	require.NoError(t, server.Start(context.Background()))
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	call := func(procedure string) error {
		return connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+server.Address())).
			CallUnary(context.Background(), connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: procedure}, &emptypb.Empty{}, &emptypb.Empty{})
	}
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(call(echoProcedure)))
	require.NoError(t, call(otherProcedure))
	_, crossed := seen.Load("admitting " + echoProcedure)
	require.False(t, crossed, "a context's interceptors never see another context's procedures")
	_, crossed = seen.Load("refusing " + otherProcedure)
	require.False(t, crossed, "a context's interceptors never see another context's procedures")
	require.Len(t, completions(t, logs), 2, "the execution log records refused and admitted calls alike")
	require.Equal(t, grpchealth.StatusServing, check(t, server.Address(), ""), "health shares the one connect.Server")
}

// A context bounds the requests of its own procedures; others are read whole.
func TestServerBoundsRequestsOfTheProceduresRegisteredWithALimit(t *testing.T) {
	server := connectrpc.New(connectrpc.Option{Addr: "127.0.0.1:0"}, discardLogger(), noShutdown{})
	const boundedProcedure, wholeProcedure = "/test.v1.UploadService/Bounded", "/test.v1.UploadService/Whole"
	accept := func(procedure string) connect.Method {
		return connect.Method{
			Spec: connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: procedure},
			Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
				if err := stream.Receive(&wrapperspb.BytesValue{}); err != nil {
					return err
				}
				return stream.Send(&emptypb.Empty{})
			},
		}
	}
	server.Register(func(s *connect.Server) { s.Register(accept(boundedProcedure)) }, connectrpc.WithReadMaxBytes(1<<10))
	server.Register(func(s *connect.Server) { s.Register(accept(wholeProcedure)) })
	require.NoError(t, server.Start(context.Background()))
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	// Random bytes stay large under compression; 5 MiB is past connect's
	// 4 MiB default.
	payload := make([]byte, 5<<20)
	_, _ = rand.Read(payload)
	call := func(procedure string) error {
		return connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+server.Address())).
			CallUnary(context.Background(), connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: procedure}, wrapperspb.Bytes(payload), &emptypb.Empty{})
	}
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(call(boundedProcedure)))
	require.NoError(t, call(wholeProcedure))
}

// Every answer names the contracts release the binary was built with, a
// refusal by a context's interceptor included; a build naming none sends no
// header.
func TestServerAnswersWithTheContractsVersion(t *testing.T) {
	server := connectrpc.New(connectrpc.Option{Addr: "127.0.0.1:0"}, discardLogger(), noShutdown{})
	refuse := func(connect.ServerFunc) connect.ServerFunc {
		return func(context.Context, connect.Spec, connect.ServerStream) error {
			return connect.NewError(connect.CodeUnauthenticated, "no identity")
		}
	}
	server.Register(func(s *connect.Server) { s.Register(echoMethod(echoProcedure)) }, connectrpc.WithInterceptors(refuse))
	require.NoError(t, server.Start(context.Background()))
	t.Cleanup(func() { _ = server.Stop(context.Background()) })

	ctx, info := connect.NewClientContext(context.Background())
	err := connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+server.Address())).CallUnary(ctx, echoSpec, &emptypb.Empty{}, &emptypb.Empty{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Equal(t, connectrpc.BuiltContractsVersion(), info.ResponseHeader().Get(connectrpc.ContractsVersionHeader))
}

// A caller that goes away fails for its own reason, whatever code the handler
// answered the cancellation with: it is no server fault.
func TestServerLogsACallerThatLeftAsCanceled(t *testing.T) {
	entered := make(chan struct{})
	server, logs := startServer(t, connectrpc.Option{Addr: "127.0.0.1:0"}, func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return connect.NewError(connect.CodeInternal, "query cut short")
	})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-entered; cancel() }()
	_ = connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+server.Address())).CallUnary(ctx, echoSpec, &emptypb.Empty{}, &emptypb.Empty{})

	require.Eventually(t, func() bool { return len(completions(t, logs)) == 1 }, time.Second, 10*time.Millisecond)
	entry := completions(t, logs)[0]
	require.Equal(t, "canceled", entry["connect_code"])
	require.Equal(t, "INFO", entry["level"])
}

// The address is required and the shutdown delay never negative.
func TestOptionValidates(t *testing.T) {
	require.Error(t, connectrpc.Option{}.Validate())
	require.Error(t, connectrpc.Option{Addr: ":8080", ShutdownDelay: -time.Second}.Validate())
	require.NoError(t, connectrpc.Option{Addr: ":8080"}.Validate())
}

// The module provides one Server whose lifecycle pkg.Lifecycle orders; in Fx
// it is reachable once started.
func TestModuleProvidesTheServer(t *testing.T) {
	var server connectrpc.Server
	app := fxtest.New(t,
		fx.NopLogger,
		fx.Supply(connectrpc.Option{Addr: "127.0.0.1:0"}, discardLogger()),
		connectrpc.Module,
		fx.Populate(&server),
		fx.Invoke(func(lc fx.Lifecycle, s *connectrpc.HTTPServer) { lc.Append(fx.StartStopHook(s.Start, s.Stop)) }),
	)
	app.RequireStart()
	require.Equal(t, grpchealth.StatusServing, check(t, server.Address(), ""))
	app.RequireStop()
}

func startServer(t *testing.T, option connectrpc.Option, handle func(context.Context) error) (*connectrpc.HTTPServer, *syncBuffer) {
	t.Helper()
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	server := connectrpc.New(option, logger, noShutdown{})
	server.Register(func(s *connect.Server) {
		s.Register(connect.Method{Spec: echoSpec, Handler: func(ctx context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&emptypb.Empty{}); err != nil {
				return err
			}
			if err := handle(ctx); err != nil {
				return err
			}
			return stream.Send(&emptypb.Empty{})
		}})
	})
	require.NoError(t, server.Start(context.Background()))
	t.Cleanup(func() { _ = server.Stop(context.Background()) })
	return server, logs
}

// echoMethod answers procedure with an empty message.
func echoMethod(procedure string) connect.Method {
	return connect.Method{
		Spec: connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: procedure},
		Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
			if err := stream.Receive(&emptypb.Empty{}); err != nil {
				return err
			}
			return stream.Send(&emptypb.Empty{})
		},
	}
}

func check(t *testing.T, address, service string) grpchealth.Status {
	t.Helper()
	client := grpchealth.NewClient(connect.NewClient(connecthttp.NewTransport(http.DefaultClient, "http://"+address, connecthttp.WithGRPC())))
	response, err := client.Check(context.Background(), &grpchealth.CheckRequest{Service: service})
	require.NoError(t, err)
	return response.Status
}

func completions(t *testing.T, logs *syncBuffer) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		if entry["msg"] == "request complete" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

type noShutdown struct{}

func (noShutdown) Shutdown(...fx.ShutdownOption) error { return nil }

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Stop does not wait for a stream whose client never leaves, and still lets
// a running unary request finish.
func TestServerStopEndsStreamsAndDrainsUnaryRequests(t *testing.T) {
	server := connectrpc.New(connectrpc.Option{Addr: "127.0.0.1:0"}, discardLogger(), noShutdown{})
	streaming, unary, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server.Handle("/stream", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done()
	}))
	server.Handle("/unary", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(unary)
		<-release
		if r.Context().Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("done"))
	}))
	require.NoError(t, server.Start(context.Background()))
	address := server.Address()

	stream, err := http.NewRequest(http.MethodPost, "http://"+address+"/stream", strings.NewReader("{}"))
	require.NoError(t, err)
	stream.Header.Set("Content-Type", "application/connect+json")
	go func() {
		if resp, err := http.DefaultClient.Do(stream); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
	}()
	<-streaming
	unaryResult := make(chan string, 1)
	go func() {
		resp, err := http.Post("http://"+address+"/unary", "application/json", strings.NewReader("{}"))
		if err != nil {
			unaryResult <- err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		unaryResult <- string(body)
	}()
	<-unary

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- server.Stop(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Stop waited for an open stream")
	}
	require.Equal(t, "done", <-unaryResult)
}

// Package connecttest serves Connect services in tests.
package connecttest

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
)

// Handler serves, over HTTP, the services register registers on one Connect
// server with the interceptors.
func Handler(register func(*connect.Server), interceptors ...connect.ServerInterceptor) http.Handler {
	server := connect.NewServer(interceptors...)
	register(server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	return mux
}

// NewServer is the process's Server on a free loopback port, not yet
// started: register handlers on it, then Start it.
func NewServer() *connectrpc.HTTPServer {
	return connectrpc.New(connectrpc.Option{Addr: "127.0.0.1:0"}, slog.New(slog.NewTextHandler(io.Discard, nil)), noShutdown{})
}

// Start starts server until the test ends and returns its base URL.
func Start(t testing.TB, server *connectrpc.HTTPServer) string {
	t.Helper()
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start server: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Stop(ctx)
	})
	return "http://" + server.Address()
}

// Serve starts a Server with what register registers on it until the test
// ends and returns its base URL.
func Serve(t testing.TB, register func(connectrpc.Server)) string {
	t.Helper()
	server := NewServer()
	register(server)
	return Start(t, server)
}

// Fronted is a Server for an Fx app under test, reached at URL from the
// moment it exists: the app's configuration often names the Server's own
// address, which the listener binds only when the app starts. Until then the
// front answers 503.
type Fronted struct {
	Server *connectrpc.HTTPServer
	// URL forwards to Server while the app runs.
	URL string

	front *httptest.Server
	proxy atomic.Pointer[httputil.ReverseProxy]
}

// NewFronted is a Fronted whose front closes when the test ends.
func NewFronted(t testing.TB) *Fronted {
	t.Helper()
	f := &Fronted{Server: NewServer()}
	f.front = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxy := f.proxy.Load()
		if proxy == nil {
			http.Error(w, "server not started", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(f.front.Close)
	f.URL = f.front.URL
	return f
}

// Option gives the app the Server as its connectrpc.Server and runs it while
// the app runs, as pkg.Lifecycle does in a process.
func (f *Fronted) Option() fx.Option {
	return fx.Options(
		fx.Provide(func() connectrpc.Server { return f.Server }),
		fx.Invoke(func(lc fx.Lifecycle) { lc.Append(fx.StartStopHook(f.start, f.Server.Stop)) }),
	)
}

// Client calls URL.
func (f *Fronted) Client() *http.Client { return f.front.Client() }

// Close closes the front; the app's stop stops the Server.
func (f *Fronted) Close() { f.front.Close() }

func (f *Fronted) start(ctx context.Context) error {
	if err := f.Server.Start(ctx); err != nil {
		return err
	}
	target, err := url.Parse("http://" + f.Server.Address())
	if err != nil {
		return err
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	// Streams reach the client as the Server writes them.
	proxy.FlushInterval = -1
	f.proxy.Store(proxy)
	return nil
}

// noShutdown ignores the shutdown a failed listener requests; the test sees
// the failure in its calls.
type noShutdown struct{}

func (noShutdown) Shutdown(...fx.ShutdownOption) error { return nil }

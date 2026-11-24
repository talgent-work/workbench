// Package connectrpc owns the process's one RPC/HTTP listener and its one
// connect.Server: the shared interceptor chain with the execution log and
// the contracts version, the contexts' own interceptors and read limits
// dispatched by procedure, grpc.health.v1 readiness and liveness, and the
// listener's start and bounded drain. Contexts register their generated
// handlers and plain HTTP handlers on it.
package connectrpc

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpchealth/v2"
	"go.uber.org/fx"
)

// LivenessService is the health service that reports SERVING for as long as
// the process runs; the empty service name reports readiness.
const LivenessService = "liveness"

// Server is what a bounded context needs to serve its contracts.
type Server interface {
	// Register serves the handlers register adds to the process's
	// connect.Server under options. A context registers each handler group
	// under its own options, and a procedure once.
	Register(register func(*connect.Server), options ...RegisterOption)
	// Handle mounts a plain HTTP handler at pattern.
	Handle(pattern string, handler http.Handler)
	// Address is the bound listen address once started.
	Address() string
}

// RegisterOption applies to the procedures one Register call adds.
type RegisterOption func(*registration)

type registration struct {
	interceptors []connect.ServerInterceptor
	readMaxBytes int
}

// WithInterceptors runs interceptors, such as the context's authentication,
// for the registered procedures, in order, inside the execution log.
func WithInterceptors(interceptors ...connect.ServerInterceptor) RegisterOption {
	return func(r *registration) { r.interceptors = append(r.interceptors, interceptors...) }
}

// WithReadMaxBytes bounds the request messages of the registered procedures;
// without it they are read whole.
func WithReadMaxBytes(limit int) RegisterOption {
	return func(r *registration) { r.readMaxBytes = limit }
}

// HTTPServer is the Server on one standard-library listener serving HTTP/1.1
// and unencrypted HTTP/2, so Connect, gRPC, gRPC-Web clients and gRPC probes
// reach the same port. pkg.Lifecycle orders its Start and Stop.
type HTTPServer struct {
	option     Option
	logger     *slog.Logger
	shutdowner fx.Shutdowner
	mux        *http.ServeMux
	health     *grpchealth.StaticChecker
	rpc        *connect.Server
	// procedures are each registered procedure's options; Register writes
	// them before Start, and calls only read them.
	procedures map[string]registration
	listener   *listener
	mounted    sync.Once
}

var _ Server = (*HTTPServer)(nil)

// Module provides the process's Server and the HTTP/2 client for the
// platform's services.
var Module = fx.Module("internal.pkg.connectrpc",
	fx.Provide(New),
	fx.Provide(func(s *HTTPServer) Server { return s }),
	fx.Provide(NewH2CClient),
)

// New builds the Server. Readiness is NOT_SERVING until Start binds the
// listener.
func New(option Option, logger *slog.Logger, shutdowner fx.Shutdowner) *HTTPServer {
	health := grpchealth.NewStaticChecker(LivenessService)
	health.SetStatus("", grpchealth.StatusNotServing)
	mux := http.NewServeMux()
	s := &HTTPServer{
		option: option, logger: logger, shutdowner: shutdowner,
		mux: mux, health: health,
		procedures: map[string]registration{},
		listener:   newListener(option.Addr, mux),
	}
	// The execution log is outermost, so it logs and recovers whatever the
	// inner interceptors do; the contracts version comes next, so every
	// answer carries it, refusals included. Health checks bypass the log.
	s.rpc = connect.NewServer(executionInterceptor(logger), NewContractsVersionInterceptor(BuiltContractsVersion()), s.dispatch)
	grpchealth.Register(s.rpc, health)
	return s
}

// Register adds the handlers to the one connect.Server and records options
// for the procedures they add; contexts call it from Fx invokes, before Start
// mounts every procedure.
func (s *HTTPServer) Register(register func(*connect.Server), options ...RegisterOption) {
	var applied registration
	for _, option := range options {
		option(&applied)
	}
	applied.interceptors = slices.Clone(applied.interceptors)
	known := map[string]bool{}
	for spec := range s.rpc.Specs() {
		known[spec.Procedure] = true
	}
	register(s.rpc)
	for spec := range s.rpc.Specs() {
		if !known[spec.Procedure] {
			s.procedures[spec.Procedure] = applied
		}
	}
}

// dispatch runs the interceptors the called procedure was registered under,
// in order, around its handler.
func (s *HTTPServer) dispatch(next connect.ServerFunc) connect.ServerFunc {
	return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
		interceptors := s.procedures[spec.Procedure].interceptors
		call := next
		for i := len(interceptors) - 1; i >= 0; i-- {
			call = interceptors[i](call)
		}
		return call(ctx, spec, stream)
	}
}

// Handle mounts a plain HTTP handler; callers call it before Start.
func (s *HTTPServer) Handle(pattern string, handler http.Handler) {
	s.mux.Handle(pattern, handler)
}

// Address is the listener's bound address once started, the configured one
// before.
func (s *HTTPServer) Address() string { return s.listener.Address() }

// The listener serves before readiness and after it: a process's own
// entry points may call its procedures over loopback, as the contexts
// composed in one process do, so the listener accepts calls from before the
// first of them starts until after the last of them drains. pkg.Lifecycle
// orders Listen and Close around the other entry points and Ready and
// Withdraw inside them; Start and Stop do both phases for a process with no
// other entry point.

// Listen mounts every registered procedure, binds the listener synchronously,
// so an address error fails startup, and serves in an owned goroutine.
// Readiness stays NOT_SERVING until Ready.
func (s *HTTPServer) Listen(context.Context) error {
	if err := s.option.Validate(); err != nil {
		return err
	}
	s.mounted.Do(func() {
		connecthttp.Mount(s.mux, s.rpc, connecthttp.WithConditionalOptions(func(spec connect.Spec) []connecthttp.Option {
			return []connecthttp.Option{connecthttp.WithReadMaxBytes(s.procedures[spec.Procedure].readMaxBytes)}
		}))
	})
	if err := s.listener.bind(); err != nil {
		return err
	}
	s.listener.serve(s.logger, s.shutdowner)
	return nil
}

// Ready reports readiness SERVING, so load balancers route to the process.
func (s *HTTPServer) Ready(context.Context) error {
	s.health.SetStatus("", grpchealth.StatusServing)
	return nil
}

// Withdraw reports readiness NOT_SERVING, keeps serving for the shutdown
// delay while load balancers stop routing, then ends the open streams so
// their clients reconnect to another replica. Unary calls, loopback ones
// included, are still served until Close. ctx bounds the delay.
func (s *HTTPServer) Withdraw(ctx context.Context) error {
	s.health.SetStatus("", grpchealth.StatusNotServing)
	if s.option.ShutdownDelay > 0 {
		timer := time.NewTimer(s.option.ShutdownDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	s.listener.endStreams()
	return nil
}

// Close drains the listener: running requests finish within ctx, and it
// stops accepting.
func (s *HTTPServer) Close(ctx context.Context) error {
	return s.listener.server.Shutdown(ctx)
}

// Start listens and reports readiness.
func (s *HTTPServer) Start(ctx context.Context) error {
	if err := s.Listen(ctx); err != nil {
		return err
	}
	return s.Ready(ctx)
}

// Stop withdraws readiness and closes the listener; ctx bounds both.
func (s *HTTPServer) Stop(ctx context.Context) error {
	return errors.Join(s.Withdraw(ctx), s.Close(ctx))
}

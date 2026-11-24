// Package connectrpc owns the process's one RPC/HTTP listener and its one
// connect.Server: the shared interceptor chain with the execution log and
// the contracts version, the contexts' own interceptors and read limits
// dispatched by procedure, grpc.health.v1 readiness and liveness, and the
// listener's start and bounded drain. Contexts register their generated
// handlers and plain HTTP handlers on it.
package connectrpc

import (
	"context"
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

// Start mounts every registered procedure, binds the listener synchronously,
// so an address error fails startup, then reports readiness SERVING and
// serves in an owned goroutine.
func (s *HTTPServer) Start(context.Context) error {
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
	s.health.SetStatus("", grpchealth.StatusServing)
	s.listener.serve(s.logger, s.shutdowner)
	return nil
}

// Stop reports readiness NOT_SERVING, keeps the listener serving for the
// shutdown delay, then drains it: unary requests finish, and streams end so
// their clients reconnect to another replica. ctx bounds the delay and the
// drain together.
func (s *HTTPServer) Stop(ctx context.Context) error {
	s.health.SetStatus("", grpchealth.StatusNotServing)
	if s.option.ShutdownDelay > 0 {
		timer := time.NewTimer(s.option.ShutdownDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
		}
	}
	return s.listener.server.Shutdown(ctx)
}

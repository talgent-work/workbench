package connectrpc

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-jimu/components/sloghelper"
	"github.com/samber/oops"
	"go.uber.org/fx"
)

// failedExitCode is the process exit code after a listener stopped on its own.
const failedExitCode = 1

// listener is the port the Server serves.
type listener struct {
	addr   string
	server *http.Server
	bound  net.Listener
	// endStreams ends every open stream; Shutdown calls it too.
	endStreams context.CancelFunc

	mu      sync.Mutex
	address string
}

// newListener serves handler over HTTP/1.1 and unencrypted HTTP/2, with
// header-read, idle and header-size limits.
func newListener(addr string, handler http.Handler) *listener {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	// Shutdown waits for running requests, but a long-lived stream ends only
	// when its client leaves; ending streams as Shutdown begins lets unary
	// requests drain while streaming clients reconnect to another replica.
	shutdown, endStreams := context.WithCancel(context.Background())
	server := &http.Server{
		Addr:              addr,
		Handler:           endStreamsOnShutdown(handler, shutdown),
		Protocols:         protocols,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     60 * time.Second,
		},
	}
	server.RegisterOnShutdown(endStreams)
	return &listener{addr: addr, server: server, address: addr, endStreams: endStreams}
}

// Address is the bound address once started, the configured one before.
func (l *listener) Address() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.address
}

// bind takes the address synchronously, so an address error fails startup.
func (l *listener) bind() error {
	bound, err := net.Listen("tcp", l.addr)
	if err != nil {
		return oops.With("addr", l.addr).Wrap(err)
	}
	l.mu.Lock()
	l.bound, l.address = bound, bound.Addr().String()
	l.mu.Unlock()
	return nil
}

// serve serves the bound address in an owned goroutine. A Serve failure
// other than a normal stop is logged once and shuts the process down with a
// failure exit code.
func (l *listener) serve(logger *slog.Logger, shutdowner fx.Shutdowner) {
	logger.Info("rpc server listening", slog.String("addr", l.Address()))
	go func() {
		err := l.server.Serve(l.bound)
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return
		}
		logger.Error("rpc server stopped; shutting down", sloghelper.Error(oops.Wrap(err)))
		if err := shutdowner.Shutdown(fx.ExitCode(failedExitCode)); err != nil {
			logger.Error("rpc server could not shut the process down", sloghelper.Error(err))
		}
	}()
}

func endStreamsOnShutdown(next http.Handler, shutdown context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isStream(r) {
			next.ServeHTTP(w, r)
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		stop := context.AfterFunc(shutdown, cancel)
		defer stop()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Connect and gRPC streaming calls and server-sent events stay open until the
// client leaves; unary calls finish on their own.
func isStream(r *http.Request) bool {
	contentType := r.Header.Get("Content-Type")
	return strings.HasPrefix(contentType, "application/connect+") ||
		strings.HasPrefix(contentType, "application/grpc") ||
		strings.Contains(r.Header.Get("Accept"), "text/event-stream")
}

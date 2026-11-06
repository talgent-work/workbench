package httpserver

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/fx"
)

type Config struct {
	Addr string `json:"addr" toml:"addr" yaml:"addr"`
}

func New(lc fx.Lifecycle, cfg Config, mux *http.ServeMux, logger *slog.Logger) *http.Server {
	// Keep HTTP/2 connections owned by net/http so Shutdown drains handlers
	// before their lifecycle-owned dependencies close.
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	// Shutdown waits for in-flight requests, but a long-lived stream only ends
	// when its client leaves. Ending streams as Shutdown begins lets unary
	// requests drain while streaming clients reconnect to another replica.
	shutdown, endStreams := context.WithCancel(context.Background())
	srv := &http.Server{
		Addr:        cfg.Addr,
		Handler:     endStreamsOnShutdown(mux, shutdown),
		Protocols:   protocols,
		IdleTimeout: 120 * time.Second,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: 30 * time.Second,
			PingTimeout:     60 * time.Second,
		},
	}
	srv.RegisterOnShutdown(endStreams)
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				return err
			}
			logger.Info("http server listening", slog.String("addr", srv.Addr))
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					logger.Error("http server error", slog.String("error", err.Error()))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})
	return srv
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

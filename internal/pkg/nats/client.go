// Package nats owns the process's shared NATS connection.
package nats

import (
	"context"
	"log/slog"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

type Config struct {
	URL string `json:"url" toml:"url" yaml:"url"`
}

// The client library reconnects a lost connection on its own, restoring its
// subscriptions and holding publications until it is back. An outage longer
// than about reconnectAttempts × reconnectWait closes the connection for good;
// the next Conn dials a new one.
const (
	connectTimeout    = 2 * time.Second
	reconnectWait     = 2 * time.Second
	reconnectJitter   = time.Second
	reconnectAttempts = 60
)

// LazyConn is the process's one NATS connection. It is dialled on first use,
// so a service starts while the broker is unavailable.
type LazyConn struct {
	mu      sync.Mutex
	cfg     Config
	logger  *slog.Logger
	nc      *natsgo.Conn
	stopped bool
}

// NewLazyConn defers dialling until the first Conn call and closes the
// connection when the process stops.
func NewLazyConn(lc fx.Lifecycle, cfg Config, logger *slog.Logger) *LazyConn {
	client := &LazyConn{cfg: cfg, logger: logger.With("component", "nats")}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return client.Close() }})
	return client
}

// Conn returns the shared connection. A connection that is reconnecting is
// returned as it is; only a closed one is replaced. The first dial does not
// wait for the broker: a broker that cannot be reached yet is retried in the
// background under the same bound as a lost connection.
func (c *LazyConn) Conn() (*natsgo.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stopped {
		return nil, natsgo.ErrConnectionClosed
	}
	if c.nc != nil && !c.nc.IsClosed() {
		return c.nc, nil
	}
	nc, err := c.connect()
	if err != nil {
		return nil, err
	}
	c.nc = nc
	return nc, nil
}

func (c *LazyConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	if c.nc != nil {
		c.nc.Close()
		c.nc = nil
	}
	return nil
}

func (c *LazyConn) connect() (*natsgo.Conn, error) {
	return natsgo.Connect(
		c.cfg.URL,
		natsgo.Timeout(connectTimeout),
		natsgo.RetryOnFailedConnect(true),
		natsgo.MaxReconnects(reconnectAttempts),
		natsgo.ReconnectWait(reconnectWait),
		natsgo.ReconnectJitter(reconnectJitter, reconnectJitter),
		natsgo.ConnectHandler(func(*natsgo.Conn) {
			c.logger.Info("NATS connected")
		}),
		natsgo.DisconnectErrHandler(func(_ *natsgo.Conn, err error) {
			c.logger.Warn("NATS disconnected; reconnecting", slog.Any("error", err))
		}),
		natsgo.ReconnectHandler(func(*natsgo.Conn) {
			c.logger.Info("NATS reconnected")
		}),
		natsgo.ClosedHandler(func(nc *natsgo.Conn) {
			c.mu.Lock()
			stopped := c.stopped
			c.mu.Unlock()
			if stopped {
				return
			}
			c.logger.Error("NATS connection closed after exhausting its reconnection attempts; the next use dials again",
				slog.Int("attempts", reconnectAttempts), slog.Any("error", nc.LastError()))
		}),
	)
}

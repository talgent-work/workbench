// Package nats owns shared NATS connection initialization.
package nats

import (
	"context"
	"sync"

	natsgo "github.com/nats-io/nats.go"
	"go.uber.org/fx"
)

type Config struct {
	URL string `json:"url" toml:"url" yaml:"url"`
}

// NewConn constructs an eager NATS connection and binds shutdown to the fx lifecycle.
func NewConn(lc fx.Lifecycle, cfg Config) (*natsgo.Conn, error) {
	nc, err := connect(cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		nc.Close()
		return nil
	}})
	return nc, nil
}

type LazyConn struct {
	mu  sync.Mutex
	cfg Config
	nc  *natsgo.Conn
}

// NewLazyConn defers NATS connection establishment until the first Conn call.
func NewLazyConn(lc fx.Lifecycle, cfg Config) *LazyConn {
	client := &LazyConn{cfg: cfg}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return client.Close() }})
	return client
}

func (c *LazyConn) Conn() (*natsgo.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nc != nil {
		switch c.nc.Status() {
		case natsgo.CLOSED, natsgo.DISCONNECTED:
			c.nc.Close()
			c.nc = nil
		default:
			return c.nc, nil
		}
	}

	nc, err := connect(c.cfg)
	if err != nil {
		return nil, err
	}
	c.nc = nc
	return nc, nil
}

func (c *LazyConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nc != nil {
		c.nc.Close()
		c.nc = nil
	}
	return nil
}

func connect(cfg Config) (*natsgo.Conn, error) {
	return natsgo.Connect(
		cfg.URL,
		natsgo.NoReconnect(),
		natsgo.RetryOnFailedConnect(false),
	)
}

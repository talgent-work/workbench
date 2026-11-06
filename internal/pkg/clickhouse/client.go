// Package clickhouse owns shared ClickHouse connection initialization.
package clickhouse

import (
	"context"
	"sync"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/fx"
)

type Config struct {
	DSN string `json:"dsn" toml:"dsn" yaml:"dsn"`
}

func NewConn(lc fx.Lifecycle, cfg Config) (chdriver.Conn, error) {
	conn, err := open(cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		return conn.Close()
	}})
	return conn, nil
}

type LazyConn struct {
	mu   sync.Mutex
	cfg  Config
	conn chdriver.Conn
}

func NewLazyConn(lc fx.Lifecycle, cfg Config) *LazyConn {
	client := &LazyConn{cfg: cfg}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return client.Close() }})
	return client
}

func (c *LazyConn) Conn(ctx context.Context) (chdriver.Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		if err := c.conn.Ping(ctx); err == nil {
			return c.conn, nil
		}
		_ = c.conn.Close()
		c.conn = nil
	}

	conn, err := open(c.cfg)
	if err != nil {
		return nil, err
	}
	c.conn = conn
	return conn, nil
}

func (c *LazyConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func open(cfg Config) (chdriver.Conn, error) {
	opts, err := clickhousego.ParseDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}
	return clickhousego.Open(opts)
}

// Package mysql owns shared MySQL xorm engine initialization.
package mysql

import (
	"context"
	"errors"
	"log/slog"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"go.uber.org/fx"
	"xorm.io/xorm"
)

const (
	defaultMaxOpenConns    = 20
	defaultMaxIdleConns    = 5
	defaultConnMaxLifetime = 30 * time.Minute
	defaultConnMaxIdleTime = 5 * time.Minute
	defaultPingTimeout     = 3 * time.Second
)

type Config struct {
	DSN             string   `json:"dsn" toml:"dsn" yaml:"dsn"`
	MaxOpenConns    int      `json:"max-open-conns" toml:"max-open-conns" yaml:"max-open-conns"`
	MaxIdleConns    int      `json:"max-idle-conns" toml:"max-idle-conns" yaml:"max-idle-conns"`
	ConnMaxLifetime Duration `json:"conn-max-lifetime" toml:"conn-max-lifetime" yaml:"conn-max-lifetime"`
	ConnMaxIdleTime Duration `json:"conn-max-idle-time" toml:"conn-max-idle-time" yaml:"conn-max-idle-time"`
	PingTimeout     Duration `json:"ping-timeout" toml:"ping-timeout" yaml:"ping-timeout"`
}

// NewEngine constructs a MySQL-backed xorm engine, verifies connectivity, and
// binds engine shutdown to the fx lifecycle.
func NewEngine(lc fx.Lifecycle, cfg Config, logger *slog.Logger) (*xorm.Engine, error) {
	if cfg.DSN == "" {
		return nil, errors.New("mysql dsn is required")
	}

	engine, err := xorm.NewEngine("mysql", cfg.DSN)
	if err != nil {
		return nil, err
	}

	db := engine.DB()
	db.SetMaxOpenConns(valueOrDefault(cfg.MaxOpenConns, defaultMaxOpenConns))
	db.SetMaxIdleConns(valueOrDefault(cfg.MaxIdleConns, defaultMaxIdleConns))
	db.SetConnMaxLifetime(durationOrDefault(cfg.ConnMaxLifetime.Duration(), defaultConnMaxLifetime))
	db.SetConnMaxIdleTime(durationOrDefault(cfg.ConnMaxIdleTime.Duration(), defaultConnMaxIdleTime))

	pingTimeout := durationOrDefault(cfg.PingTimeout.Duration(), defaultPingTimeout)
	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = engine.Close()
		return nil, err
	}

	if logger != nil {
		logger.Info("mysql connected")
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		if logger != nil {
			logger.Info("mysql closing")
		}
		return engine.Close()
	}})

	return engine, nil
}

func valueOrDefault(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func durationOrDefault(value time.Duration, fallback time.Duration) time.Duration {
	if value == 0 {
		return fallback
	}
	return value
}

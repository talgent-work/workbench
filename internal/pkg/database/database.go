// Package database owns the Workbench's MySQL databases: one xorm engine per
// named database, the go-jimu xorm adapter repositories resolve their session
// through, and that database's transaction Manager, which Applications
// receive as transaction.Transactor.
//
// A process lists Module once per database it opens; a bounded context lists
// Bind for the one database it uses. Repositories call DB.Session for each
// statement and run a write of several statements in the Manager's
// WithinOrJoin. The pools stay open until the process exits, so no repository
// or Domain Event handler loses its database while the process drains.
package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-jimu/components/transaction"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	_ "github.com/go-sql-driver/mysql" // registers the mysql driver
	"github.com/samber/oops"
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

// Option configures one database's connection pool; zero values take the
// defaults above.
type Option struct {
	DSN             string   `json:"dsn" toml:"dsn" yaml:"dsn"`
	MaxOpenConns    int      `json:"max-open-conns" toml:"max-open-conns" yaml:"max-open-conns"`
	MaxIdleConns    int      `json:"max-idle-conns" toml:"max-idle-conns" yaml:"max-idle-conns"`
	ConnMaxLifetime Duration `json:"conn-max-lifetime" toml:"conn-max-lifetime" yaml:"conn-max-lifetime"`
	ConnMaxIdleTime Duration `json:"conn-max-idle-time" toml:"conn-max-idle-time" yaml:"conn-max-idle-time"`
	PingTimeout     Duration `json:"ping-timeout" toml:"ping-timeout" yaml:"ping-timeout"`
}

// Validate requires a DSN and non-negative pool settings.
func (o Option) Validate() error {
	if strings.TrimSpace(o.DSN) == "" {
		return errors.New("database: dsn is required")
	}
	if o.MaxOpenConns < 0 || o.MaxIdleConns < 0 || o.ConnMaxLifetime < 0 || o.ConnMaxIdleTime < 0 || o.PingTimeout < 0 {
		return errors.New("database: pool settings must not be negative")
	}
	return nil
}

// Open connects one database and verifies it answers.
func Open(option Option, logger *slog.Logger) (*jimuxorm.DB, *xorm.Engine, error) {
	if err := option.Validate(); err != nil {
		return nil, nil, err
	}
	engine, err := xorm.NewEngine("mysql", option.DSN)
	if err != nil {
		return nil, nil, oops.Wrap(err)
	}
	pool := engine.DB()
	pool.SetMaxOpenConns(orDefault(option.MaxOpenConns, defaultMaxOpenConns))
	pool.SetMaxIdleConns(orDefault(option.MaxIdleConns, defaultMaxIdleConns))
	pool.SetConnMaxLifetime(orDefault(option.ConnMaxLifetime.Duration(), defaultConnMaxLifetime))
	pool.SetConnMaxIdleTime(orDefault(option.ConnMaxIdleTime.Duration(), defaultConnMaxIdleTime))
	ctx, cancel := context.WithTimeout(context.Background(), orDefault(option.PingTimeout.Duration(), defaultPingTimeout))
	defer cancel()
	if err := pool.PingContext(ctx); err != nil {
		_ = engine.Close()
		return nil, nil, oops.Wrap(err)
	}
	db, err := jimuxorm.New(engine)
	if err != nil {
		_ = engine.Close()
		return nil, nil, err
	}
	logger.Info("mysql connected")
	return db, engine, nil
}

// Module opens the database named name; process composition supplies its
// Option under the same name. It is listed before the bounded contexts so the
// connection is checked at startup.
func Module(name string) fx.Option {
	tag := nameTag(name)
	return fx.Module("internal.pkg.database."+name,
		fx.Provide(fx.Annotate(Open, fx.ParamTags(tag, ""), fx.ResultTags(tag, tag))),
		fx.Provide(fx.Annotate(
			func(db *jimuxorm.DB) transaction.Transactor { return db.Manager() },
			fx.ParamTags(tag), fx.ResultTags(tag),
		)),
		fx.Invoke(fx.Annotate(func(*jimuxorm.DB) {}, fx.ParamTags(tag))),
	)
}

// Bind makes the database named name the only one a bounded context's
// repositories and Applications see; it opens nothing.
func Bind(name string) fx.Option {
	tag := nameTag(name)
	return fx.Provide(
		fx.Annotate(func(db *jimuxorm.DB) *jimuxorm.DB { return db }, fx.ParamTags(tag)),
		fx.Annotate(func(engine *xorm.Engine) *xorm.Engine { return engine }, fx.ParamTags(tag)),
		fx.Annotate(func(t transaction.Transactor) transaction.Transactor { return t }, fx.ParamTags(tag)),
		fx.Private,
	)
}

func nameTag(name string) string { return fmt.Sprintf(`name:%q`, name) }

func orDefault[T int | time.Duration](value, fallback T) T {
	if value == 0 {
		return fallback
	}
	return value
}

package pkg

import (
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-jimu/components/sloghelper"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/nats"
	"github.com/talgent-work/workbench/internal/pkg/redis"
)

var LoggerModule = fx.Module(
	"internal.pkg.logger",
	fx.Provide(sloghelper.NewLog),
)

// Infrastructure modules construct their clients where a process lists them,
// before any business module. Fx stops hooks in reverse registration order, so
// these clients close only after every business module that uses them has
// stopped and drained its Domain Events.

var RedisModule = fx.Module(
	"internal.pkg.redis",
	fx.Provide(redis.NewClient),
	fx.Invoke(func(*goredis.Client) {}),
)

var ClickHouseModule = fx.Module(
	"internal.pkg.clickhouse",
	fx.Provide(clickhouse.NewConn),
	fx.Invoke(func(chdriver.Conn) {}),
)

var LazyClickHouseModule = fx.Module(
	"internal.pkg.clickhouse.lazy",
	fx.Provide(clickhouse.NewLazyConn),
	fx.Invoke(func(*clickhouse.LazyConn) {}),
)

var NATSLazyModule = fx.Module(
	"internal.pkg.nats.lazy",
	fx.Provide(nats.NewLazyConn),
	fx.Invoke(func(*nats.LazyConn) {}),
)

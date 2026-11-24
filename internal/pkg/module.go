package pkg

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/go-jimu/components/sloghelper"
	goredis "github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/fx"
	"golang.org/x/net/http2"

	"github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/httpserver"
	"github.com/talgent-work/workbench/internal/pkg/nats"
	"github.com/talgent-work/workbench/internal/pkg/otelsetup"
	"github.com/talgent-work/workbench/internal/pkg/redis"
)

// MetricsModule provides the service's MeterProvider, exporting to the OTLP
// collector when one is configured, and flushes it on stop.
func MetricsModule(serviceName string) fx.Option {
	return fx.Module(
		"internal.pkg.metrics",
		fx.Provide(func(lc fx.Lifecycle) (metric.MeterProvider, error) {
			provider, shutdown, err := otelsetup.NewMeterProvider(context.Background(), serviceName)
			if err != nil {
				return nil, err
			}
			lc.Append(fx.StopHook(shutdown))
			return provider, nil
		}),
	)
}

var LoggerModule = fx.Module(
	"internal.pkg.logger",
	fx.Provide(sloghelper.NewLog),
)

var HTTPModule = fx.Module(
	"internal.pkg.http",
	fx.Provide(newServeMux),
	fx.Provide(httpserver.New),
	fx.Invoke(func(*http.Server) {}),
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

var H2CClientModule = fx.Module(
	"internal.pkg.h2c.client",
	fx.Provide(NewH2CClient),
)

func newServeMux() *http.ServeMux {
	return http.NewServeMux()
}

func NewH2CClient() *http.Client {
	return &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return net.Dial(network, addr)
			},
		},
	}
}

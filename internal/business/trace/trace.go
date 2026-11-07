package trace

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/talgent-work/workbench/gen/go/talgent/trace/public/v1/tracepublicv1connect"

	"connectrpc.com/connect"
	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/business/trace/infrastructure/access"
	"github.com/talgent-work/workbench/internal/business/trace/infrastructure/clickhouse"
	tracerpc "github.com/talgent-work/workbench/internal/business/trace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/connectlog"
)

// ClickHouseConfig addresses the native telemetry database, apart from any
// other ClickHouse connection of the process hosting Trace.
type ClickHouseConfig struct {
	DSN string `json:"dsn" toml:"dsn" yaml:"dsn"`
}

// Validate requires the telemetry database; Trace has no fallback to another.
func (c ClickHouseConfig) Validate() error {
	if strings.TrimSpace(c.DSN) == "" {
		return errors.New("dsn is required")
	}
	return nil
}

// AccessConfig reaches the Project access decision and Agent's Work access.
type AccessConfig = access.Config

type AuthnConfig struct {
	Enabled string `json:"enabled" toml:"enabled" yaml:"enabled"`
	Mode    string `json:"mode" toml:"mode" yaml:"mode"`
}

func (c AuthnConfig) EnabledFlag() bool {
	switch strings.TrimSpace(c.Enabled) {
	case "", "true", "TRUE", "True", "1", "yes", "YES", "Yes", "on", "ON", "On":
		return true
	default:
		return false
	}
}

// Module runs inside controlplane; its telemetry connection stays
// private so it cannot be mistaken for the host's other ClickHouse use.
var Module = fx.Module(
	"domain.trace",
	fx.Provide(fx.Private, func(lc fx.Lifecycle, cfg ClickHouseConfig) (chdriver.Conn, error) {
		return pkgclickhouse.NewConn(lc, pkgclickhouse.Config{DSN: cfg.DSN})
	}),
	fx.Provide(fx.Annotate(clickhouse.NewRepository,
		fx.As(new(query.SpanQueryRepository)),
		fx.As(new(query.TraceMetricRepository)),
		fx.As(new(query.EventRepository)),
	)),
	fx.Provide(fx.Annotate(
		func(cfg AuthnConfig) connect.Interceptor {
			return authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{
				Enabled: cfg.EnabledFlag(),
			})
		},
		fx.ResultTags(`group:"trace_connect_interceptors"`),
	)),
	fx.Provide(
		access.NewProjectAccess,
		fx.Annotate(access.NewWorkAccess, fx.As(new(query.WorkAccess))),
	),
	fx.Provide(query.NewQueries),
	fx.Provide(application.NewApplication),

	fx.Invoke(func(app *application.Application, mux *http.ServeMux, logger *slog.Logger, in connectHandlerInterceptors) {
		opts := make([]connect.HandlerOption, 0, 1)
		interceptors := append([]connect.Interceptor{connectlog.NewInterceptor(logger)}, in.Interceptors...)
		opts = append(opts, connect.WithInterceptors(interceptors...))
		path, handler := tracepublicv1connect.NewTraceQueryServiceHandler(tracerpc.NewHandler(app), opts...)
		mux.Handle(path, handler)
	}),
)

type connectHandlerInterceptors struct {
	fx.In

	Interceptors []connect.Interceptor `group:"trace_connect_interceptors"`
}

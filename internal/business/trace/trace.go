package trace

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/talgent-work/contracts/gen/go/trace/public/v1/tracepublicv1connect"

	chdriver "github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/business/trace/infrastructure/access"
	"github.com/talgent-work/workbench/internal/business/trace/infrastructure/clickhouse"
	tracerpc "github.com/talgent-work/workbench/internal/business/trace/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
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

// AccessConfig reaches the Project access decision, Agent's Work access and
// Work attribution, and Account's Application Ownership Query.
type AccessConfig = access.Config

// Module runs inside controlplane; its telemetry connection stays
// private so it cannot be mistaken for the host's other ClickHouse use.
var Module = fx.Module(
	"domain.trace",
	fx.Provide(fx.Private, func(lc fx.Lifecycle, cfg ClickHouseConfig) (chdriver.Conn, error) {
		return pkgclickhouse.NewConn(lc, pkgclickhouse.Config{DSN: cfg.DSN})
	}),
	fx.Provide(fx.Annotate(clickhouse.NewRepository,
		fx.As(new(query.NativeEvidenceQueryRepository)),
		fx.As(new(query.MeasurementQueryRepository)),
	)),
	fx.Provide(
		access.NewProjectAccess,
		fx.Annotate(access.NewAgent, fx.As(new(query.AgentPort))),
		fx.Annotate(access.NewAccount, fx.As(new(query.AccountPort))),
	),
	fx.Provide(query.NewQueries),
	fx.Provide(application.NewApplication),

	fx.Invoke(func(app *application.Application, mux *http.ServeMux, logger *slog.Logger) {
		server := connectserver.New(logger, authn.NewPrincipalInterceptor())
		tracepublicv1connect.RegisterTraceQueryServiceHandler(server, tracerpc.NewHandler(app))
		connectserver.Mount(mux, server)
	}),
)

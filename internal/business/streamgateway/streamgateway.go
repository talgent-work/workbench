// Package streamgateway wires the streamgateway service into fx.
package streamgateway

import (
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure/unifiedmessagelog"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/connectlog"
)

// AgentConfig reaches Agent's private Work access API.
type AgentConfig struct {
	Address      string `json:"address" yaml:"address" toml:"address"`
	ControlToken string `json:"control-token" yaml:"control-token" toml:"control-token"`
}

// Module assembles streamgateway. Runtime modules in cmd/streamgateway provide
// ClickHouse, NATS, logging, and HTTP dependencies.
var Module = fx.Module(
	"service.streamgateway",
	fx.Provide(unifiedmessagelog.NewLazyClickHouseReader),
	fx.Provide(fx.Annotate(
		func(client *http.Client, cfg AgentConfig) (*infrastructure.WorkAccess, error) {
			return infrastructure.NewWorkAccess(client, cfg.Address, cfg.ControlToken)
		},
		fx.As(new(query.WorkAccessPort)),
		fx.As(new(query.StreamAccessPort)),
	)),
	fx.Provide(fx.Annotate(
		infrastructure.NewPortalLiveStreamNATS,
		fx.As(new(query.PortalLiveStream)),
	)),
	fx.Provide(query.NewWatchStream, query.NewListMessagesBySessionHandler, application.NewApplication),
	fx.Invoke(func(app *application.Application, mux *http.ServeMux, logger *slog.Logger) {
		path, handler := streamgatewaypublicv1connect.NewStreamGatewayServiceHandler(
			streamrpc.NewHandler(app),
			connect.WithInterceptors(connectlog.NewInterceptor(logger), authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})),
		)
		mux.Handle(path, handler)
	}),
)

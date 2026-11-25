// Package streamgateway wires the streamgateway service into fx.
package streamgateway

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure"
	"github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure/unifiedmessagelog"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
)

// AgentConfig reaches Agent's private Work access API.
type AgentConfig struct {
	Address      string `json:"address" yaml:"address" toml:"address"`
	ControlToken string `json:"control-token" yaml:"control-token" toml:"control-token"`
}

// LiveConfig bounds the live streams one process serves; a zero field takes
// its default.
type LiveConfig = query.WatchStreamOptions

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
	fx.Provide(fx.Annotate(
		infrastructure.NewLiveDropCounter,
		fx.As(new(query.LiveDropRecorder)),
	)),
	fx.Provide(query.NewWatchStream, query.NewListMessagesBySessionHandler, application.NewApplication),
	fx.Invoke(func(app *application.Application, server connectrpc.Server) {
		server.Register(func(s *connect.Server) {
			streamgatewaypublicv1connect.RegisterStreamGatewayServiceHandler(s, streamrpc.NewHandler(app))
		}, connectrpc.WithInterceptors(authn.NewPrincipalInterceptor()))
	}),
)

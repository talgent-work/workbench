package agent

import (
	"log/slog"
	"net/http"

	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/turnresults"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
)

// TurnResultModule serves the Work queries answered from the durable message
// log: the Work Turn Result Query Contract, and the call positions the Work
// Context Position Query names. It requires a lazy ClickHouse connection, so
// only a process that composes Module with it reads them.
var TurnResultModule = fx.Module("agent.turn_results",
	fx.Provide(
		fx.Annotate(turnresults.NewReader, fx.As(new(query.RootTurnResultReader)), fx.As(new(query.CallPositionReader))),
		query.NewWorkTurnResults,
	),
	fx.Invoke(registerTurnResultHandler),
)

func registerTurnResultHandler(mux *http.ServeMux, results *query.WorkTurnResults, config RPCConfig, logger *slog.Logger) {
	server := connectserver.New(logger)
	agentprivatev1connect.RegisterWorkTurnResultServiceHandler(server, transport.NewTurnResultHandler(results, config.ControlToken))
	connectserver.Mount(mux, server)
}

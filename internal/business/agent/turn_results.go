package agent

import (
	"log/slog"
	"net/http"

	"connectrpc.com/connect"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/turnresults"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/pkg/connectlog"
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
	path, handler := agentprivatev1connect.NewWorkTurnResultServiceHandler(transport.NewTurnResultHandler(results, config.ControlToken), connect.WithInterceptors(connectlog.NewInterceptor(logger)))
	mux.Handle(path, handler)
}

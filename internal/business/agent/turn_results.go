package agent

import (
	"connectrpc.com/connect/v2"
	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/turnresults"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/pkg/connectrpc"
)

// TurnResultModule serves the Work queries answered from the durable message
// log: the Work Turn Result Query Contract, and the call positions the Work
// Context Position Query names. It requires a lazy ClickHouse connection, so
// only a process that composes Module with it reads them.
var TurnResultModule = fx.Module("agent.turn_results",
	fx.Provide(
		fx.Annotate(turnresults.NewReader, fx.As(new(query.DispatcherMessageLogPort))),
		query.NewWorkTurnResults,
	),
	fx.Invoke(registerTurnResultHandler),
)

func registerTurnResultHandler(server connectrpc.Server, results *query.WorkTurnResults, config RPCConfig) {
	server.Register(func(s *connect.Server) {
		agentprivatev1connect.RegisterWorkTurnResultServiceHandler(s, transport.NewTurnResultHandler(results, config.ControlToken))
	})
}

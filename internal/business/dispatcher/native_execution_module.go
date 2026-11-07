package dispatcher

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"go.uber.org/fx"

	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/peer"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	dispatchertransport "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/transport/routing"
	"github.com/talgent-work/workbench/internal/pkg/connectserver"
)

type NativeExecutionConfig struct {
	ControlToken string `json:"control-token" toml:"control-token" yaml:"control-token"`
}

func (c NativeExecutionConfig) Validate() error {
	if strings.TrimSpace(c.ControlToken) == "" {
		return errors.New("dispatcher: native execution control token required")
	}
	return nil
}

var nativeExecutionModule = fx.Module("dispatcher.native-execution",
	fx.Provide(fx.Annotate(newNativeExecutionsRuntime, fx.As(new(command.ExecutionConnectionPort)), fx.As(new(application.HarnessSettingsPort)), fx.As(new(application.PendingInteractionsPort)), fx.As(new(application.TurnControlPort)), fx.As(new(application.CapabilityReaderPort)), fx.As(new(application.ProcessObservationReader)))),
	fx.Provide(command.NewConnectExecutionHandler, command.NewEnqueueInputHandler, command.NewDropInputHandler, command.NewStopExecutionHandler, application.NewApplication),
	fx.Invoke(func(app *application.Application, cfg NativeExecutionConfig, mux *http.ServeMux, logger *slog.Logger, ownership *dispatcherredis.Ownership, client *http.Client) {
		local := dispatchertransport.NewExecutionHandler(app, cfg.ControlToken)
		routed := routing.NewNativeExecutionHandler(local, ownership, ownership.Self(), cfg.ControlToken, peer.NewNativeClients(client))
		server := connectserver.New(logger)
		dispatcherprivatev1connect.RegisterExecutionServiceHandler(server, routed)
		connectserver.Mount(mux, server)
	}),
)

func newNativeExecutionsRuntime(lc fx.Lifecycle, feedback command.ExecutionFeedbackReporter, delivery *messagedelivery.Sink, ownership *dispatcherredis.Ownership, buffer *dispatcherredis.InputBuffer, timing OwnershipConfig, logger *slog.Logger, config NativeExecutionConfig, feedbackConfig AgentFeedbackConfig) (*NativeExecutions, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(feedbackConfig.Addr) == "" || strings.TrimSpace(feedbackConfig.Token) == "" {
		return nil, errors.New("dispatcher: Agent feedback address and token required")
	}
	// Execd process HTTP endpoints use HTTP/1.1, independently of internal RPC.
	manager, err := NewNativeExecutions(http.DefaultClient, feedback, delivery, ownership, buffer, timing, logger)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStart: manager.Start, OnStop: manager.Shutdown})
	return manager, nil
}

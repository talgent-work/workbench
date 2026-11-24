package agent

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
)

// ApplicationRemovalConfig names Agent's own consumer group for Account's
// Application Removed, so its progress never depends on another module's
// consumers.
type ApplicationRemovalConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultApplicationRemovalGroup = "agent-application-removal"

// ApplicationRemovalModule consumes Application Removed and closes every
// unclosed Work of the removed Application, each by its own bounded task
// (Close removed Application's Works). Without it, those Works stay open,
// undrivable, until their idle timeout stops them. It requires RecoveryModule,
// whose queue runs the closes, and the process's message bus Option.
var ApplicationRemovalModule = fx.Module("agent.application_removal",
	fx.Provide(fx.Annotate(newApplicationRemovalConsumer, fx.ResultTags(messagebus.ConsumerTag))),
)

func newApplicationRemovalConsumer(cfg ApplicationRemovalConfig, bus messagebus.Option, shutdowner fx.Shutdowner, recovery *application.Recovery, logger *slog.Logger) (*messagebus.Consumer, error) {
	return messagebus.NewConsumer(bus, consumerGroup(cfg.ConsumerGroup, defaultApplicationRemovalGroup), shutdowner, logger, messagehandler.NewApplicationRemovalHandler(recovery))
}

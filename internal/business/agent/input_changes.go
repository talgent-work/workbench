package agent

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
)

// InputChangeConfig names Agent's own consumer group for Storage's file
// changes, so their progress never depends on another module's consumers.
type InputChangeConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultInputChangeGroup = "agent-input-changes"

// InputChangeModule consumes the File Change Published Fact: the Works on
// Managed Computers whose inputs changed receive their current inputs, and
// every unclosed Work reading them is told of the change by its own bounded
// task (Notify changed inputs). It requires RecoveryModule, whose queue runs
// those tasks, and the process's message bus Option.
var InputChangeModule = fx.Module("agent.input_changes",
	fx.Provide(fx.Annotate(newInputChangeConsumer, fx.ResultTags(messagebus.ConsumerTag))),
)

func newInputChangeConsumer(cfg InputChangeConfig, bus messagebus.Option, shutdowner fx.Shutdowner, recovery *application.Recovery, logger *slog.Logger) (*messagebus.Consumer, error) {
	return messagebus.NewConsumer(bus, consumerGroup(cfg.ConsumerGroup, defaultInputChangeGroup), shutdowner, logger, messagehandler.NewFileChangedHandler(recovery))
}

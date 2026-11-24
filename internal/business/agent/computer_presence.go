package agent

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
)

// ComputerPresenceConfig names Agent's own consumer group for Computer's
// Presence changes, so their progress never depends on another module's
// consumers.
type ComputerPresenceConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultComputerPresenceGroup = "agent-computer-presence"

// ComputerPresenceModule consumes Computer Became Unreachable and Computer
// Became Reachable and records or ends the Disconnection of the Works running
// on that Managed Computer, reconciling a reconnected machine and
// retrying the Resumes owed on its return, and Computer Removed, which loses
// the execution environment of every Work on it. Without it, each Work's
// recovery still reads its Computer's Presence and removal and retries an
// owed Resume, only later. It requires RecoveryModule, for the reconnection,
// and the process's message bus Option.
var ComputerPresenceModule = fx.Module("agent.computer_presence",
	fx.Provide(fx.Annotate(newComputerPresenceConsumer, fx.ResultTags(messagebus.ConsumerTag))),
)

func newComputerPresenceConsumer(cfg ComputerPresenceConfig, bus messagebus.Option, shutdowner fx.Shutdowner, reach *command.ComputerReachHandler, reconnection *command.ComputerReconnectionHandler, logger *slog.Logger) (*messagebus.Consumer, error) {
	return messagebus.NewConsumer(bus, consumerGroup(cfg.ConsumerGroup, defaultComputerPresenceGroup), shutdowner, logger, messagehandler.NewComputerPresenceHandler(reach, reconnection))
}

package agent

import (
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
)

// DeskAssignmentConfig names Agent's own consumer group for Computer's Desk
// Assigned facts, so their progress never depends on another module's
// consumers.
type DeskAssignmentConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultDeskAssignmentGroup = "agent-desk-assignments"

// DeskAssignmentModule consumes the Desk Assigned Published Fact and seats
// the execution whose Desk was seated; a fact that overtook the commit of its
// execution is handled again in place. Without it, the execution's
// reconciliation still finds the seated Desk by asking Computer. It requires
// the process's message bus Option.
var DeskAssignmentModule = fx.Module("agent.desk_assignments",
	fx.Provide(func() func() time.Time { return time.Now }, fx.Private),
	fx.Provide(command.NewSeatExecutionHandler, fx.Private),
	fx.Provide(fx.Annotate(newDeskAssignmentConsumer, fx.ResultTags(messagebus.ConsumerTag))),
)

func newDeskAssignmentConsumer(cfg DeskAssignmentConfig, bus messagebus.Option, shutdowner fx.Shutdowner, seating *command.SeatExecutionHandler, logger *slog.Logger) (*messagebus.Consumer, error) {
	return messagebus.NewConsumer(bus, consumerGroup(cfg.ConsumerGroup, defaultDeskAssignmentGroup), shutdowner, logger, messagehandler.NewDeskAssignedHandler(seating))
}

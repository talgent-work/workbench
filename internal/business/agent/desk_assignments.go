package agent

import (
	"log/slog"
	"strings"

	"github.com/go-jimu/components/ddd/message"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	pkgintegrationmessage "github.com/talgent-work/workbench/pkg/integrationmessage"
	pkgkafka "github.com/talgent-work/workbench/pkg/kafka"
)

// DeskAssignmentConfig names Agent's own consumer group for Computer's Desk
// Assigned facts, so their progress never depends on another module's
// consumers.
type DeskAssignmentConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultDeskAssignmentGroup = "agent-desk-assignments"

// DeskAssignmentModule consumes the Desk Assigned Published Fact and begins
// the waiting start of the Work whose Desk was seated; a fact that overtook
// the commit of its start is handled again in place. Without it,
// the Work's recovery still finds the seated Desk by asking Computer. It
// requires a Kafka consumer factory.
var DeskAssignmentModule = fx.Module("agent.desk_assignments",
	fx.Invoke(consumeDeskAssignments),
)

func consumeDeskAssignments(lc fx.Lifecycle, shutdowner fx.Shutdowner, cfg DeskAssignmentConfig, factory *pkgkafka.ConsumerFactory, resolver message.PayloadResolver, seating *command.SeatWaitingStartHandler, logger *slog.Logger) error {
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = defaultDeskAssignmentGroup
	}
	handler := messagehandler.NewDeskAssignedHandler(seating)
	consumer, err := pkgintegrationmessage.NewConsumer(pkgintegrationmessage.Config{ConsumerGroup: group}, factory, resolver, logger, pkgintegrationmessage.IntegrationTopics(handler.Listening()))
	if err != nil {
		return err
	}
	if err := consumer.Subscribe(handler); err != nil {
		return err
	}
	pkgintegrationmessage.RunWithLifecycle(lc, consumer, shutdowner, logger)
	return nil
}

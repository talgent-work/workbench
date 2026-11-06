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
// and a Kafka consumer factory.
var ComputerPresenceModule = fx.Module("agent.computer_presence",
	fx.Invoke(consumeComputerPresence),
)

func consumeComputerPresence(lc fx.Lifecycle, shutdowner fx.Shutdowner, cfg ComputerPresenceConfig, factory *pkgkafka.ConsumerFactory, resolver message.PayloadResolver, reach *command.ComputerReachHandler, reconnection *command.ComputerReconnectionHandler, logger *slog.Logger) error {
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = defaultComputerPresenceGroup
	}
	handler := messagehandler.NewComputerPresenceHandler(reach, reconnection)
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

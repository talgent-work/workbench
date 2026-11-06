package agent

import (
	"log/slog"
	"strings"

	"github.com/go-jimu/components/ddd/message"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	pkgintegrationmessage "github.com/talgent-work/workbench/pkg/integrationmessage"
	pkgkafka "github.com/talgent-work/workbench/pkg/kafka"
)

// InputChangeConfig names Agent's own consumer group for Storage's file
// changes, so their progress never depends on another module's consumers.
type InputChangeConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultInputChangeGroup = "agent-input-changes"

// InputChangeModule consumes the File Change Published Fact and hands the
// Works on Managed Computers whose input source changed their current
// inputs. It requires RecoveryModule and a Kafka consumer factory.
var InputChangeModule = fx.Module("agent.input_changes",
	fx.Invoke(consumeInputChanges),
)

func consumeInputChanges(lc fx.Lifecycle, shutdowner fx.Shutdowner, cfg InputChangeConfig, factory *pkgkafka.ConsumerFactory, resolver message.PayloadResolver, recovery *application.Recovery, logger *slog.Logger) error {
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = defaultInputChangeGroup
	}
	handler := messagehandler.NewFileChangedHandler(recovery)
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

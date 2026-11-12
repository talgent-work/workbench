package agent

import (
	"log/slog"
	"strings"

	"github.com/go-jimu/components/ddd/message"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	pkgintegrationmessage "github.com/talgent-work/workbench/internal/pkg/integrationmessage"
	pkgkafka "github.com/talgent-work/workbench/internal/pkg/kafka"
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
// whose queue runs the closes, and a Kafka consumer factory.
var ApplicationRemovalModule = fx.Module("agent.application_removal",
	fx.Invoke(consumeApplicationRemovals),
)

func consumeApplicationRemovals(lc fx.Lifecycle, shutdowner fx.Shutdowner, cfg ApplicationRemovalConfig, factory *pkgkafka.ConsumerFactory, resolver message.PayloadResolver, recovery *application.Recovery, logger *slog.Logger) error {
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = defaultApplicationRemovalGroup
	}
	handler := messagehandler.NewApplicationRemovalHandler(recovery)
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

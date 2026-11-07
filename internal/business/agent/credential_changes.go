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

// CredentialChangeConfig names Agent's own consumer group for Marketplace's
// credential changes, so their progress never depends on another module's
// consumers.
type CredentialChangeConfig struct {
	ConsumerGroup string `json:"consumer-group" yaml:"consumer-group" toml:"consumer-group"`
}

const defaultCredentialChangeGroup = "agent-credential-changes"

// CredentialChangeModule consumes the Credential Change Published Fact and
// replaces the delivered credentials of the Works using the changed
// authorization. It requires RecoveryModule and a Kafka consumer factory.
var CredentialChangeModule = fx.Module("agent.credential_changes",
	fx.Invoke(consumeCredentialChanges),
)

func consumeCredentialChanges(lc fx.Lifecycle, shutdowner fx.Shutdowner, cfg CredentialChangeConfig, factory *pkgkafka.ConsumerFactory, resolver message.PayloadResolver, recovery *application.Recovery, logger *slog.Logger) error {
	group := strings.TrimSpace(cfg.ConsumerGroup)
	if group == "" {
		group = defaultCredentialChangeGroup
	}
	handler := messagehandler.NewCredentialChangedHandler(recovery)
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

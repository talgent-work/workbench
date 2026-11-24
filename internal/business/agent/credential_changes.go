package agent

import (
	"log/slog"
	"strings"

	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/transport/messagehandler"
	"github.com/talgent-work/workbench/internal/pkg/messagebus"
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
// authorization. It requires RecoveryModule and the process's message bus Option.
var CredentialChangeModule = fx.Module("agent.credential_changes",
	fx.Provide(fx.Annotate(newCredentialChangeConsumer, fx.ResultTags(messagebus.ConsumerTag))),
)

func newCredentialChangeConsumer(cfg CredentialChangeConfig, bus messagebus.Option, shutdowner fx.Shutdowner, recovery *application.Recovery, logger *slog.Logger) (*messagebus.Consumer, error) {
	return messagebus.NewConsumer(bus, consumerGroup(cfg.ConsumerGroup, defaultCredentialChangeGroup), shutdowner, logger, messagehandler.NewCredentialChangedHandler(recovery))
}

// consumerGroup is the configured consumer group of one of Agent's consumers,
// or its default.
func consumerGroup(configured, fallback string) string {
	if group := strings.TrimSpace(configured); group != "" {
		return group
	}
	return fallback
}

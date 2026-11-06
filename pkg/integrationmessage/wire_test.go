package integrationmessage_test

import (
	"log/slog"
	"testing"

	"github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	pkgintegrationmessage "github.com/talgent-work/workbench/pkg/integrationmessage"
	pkgkafka "github.com/talgent-work/workbench/pkg/kafka"
)

// Integration-message config must own consumer group semantics, not Kafka
// broker transport settings.
func TestConfigYAMLUnmarshal(t *testing.T) {
	raw := []byte("consumer_group: supervisor-service\n")

	var cfg pkgintegrationmessage.Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, "supervisor-service", cfg.ConsumerGroup)
}

// IntegrationTopics must convert message kinds into topic names while skipping
// empty kinds that should not be subscribed, naming a fact two contexts
// consume once.
func TestIntegrationTopicsStrings(t *testing.T) {
	topics := pkgintegrationmessage.IntegrationTopics{
		message.Kind("work.started"),
		"",
		message.Kind("work.ended"),
		message.Kind("work.started"),
	}

	require.Equal(t, []string{"work.started", "work.ended"}, topics.Strings())
}

// Intent: the consumer accepts the factory's client. The Kafka consumer
// refuses at startup a client that commits on its own or rebalances while a
// batch is still being handled, and one without a payload resolver.
func TestNewConsumerAcceptsTheFactoryClient(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	factory := pkgkafka.NewConsumerFactory(pkgkafka.Config{Brokers: []string{"127.0.0.1:9092"}}, discard)

	consumer, err := pkgintegrationmessage.NewConsumer(
		pkgintegrationmessage.Config{ConsumerGroup: "group-a"},
		factory,
		pkgintegrationmessage.NewProtoPayloadResolver(),
		discard,
		pkgintegrationmessage.IntegrationTopics{"work.started"},
	)
	require.NoError(t, err)
	consumer.Close()
}

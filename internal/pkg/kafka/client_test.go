package kafka

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

// Kafka middleware config must deserialize service broker settings without
// integration-message packages owning the transport shape.
func TestConfigYAMLUnmarshal(t *testing.T) {
	raw := []byte("brokers:\n  - 127.0.0.1:9092\n")

	var cfg Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, []string{"127.0.0.1:9092"}, cfg.Brokers)
}

// NewProducer must own the shared Kafka producer client lifecycle so fx
// shutdown releases the middleware connection.
func TestNewProducerRegistersLifecycleHook(t *testing.T) {
	lc := &fxLifecycle{}

	client, err := NewProducer(lc, Config{Brokers: []string{"127.0.0.1:9092"}}, slog.New(slog.DiscardHandler))
	require.NoError(t, err)
	require.NotNil(t, client)
	t.Cleanup(client.Close)
	require.Len(t, lc.hooks, 1)
	require.NotNil(t, lc.hooks[0].OnStop)

	require.NoError(t, lc.hooks[0].OnStop(context.Background()))
}

// ConsumerFactory must create raw Kafka consumer clients from shared broker
// config while higher-level adapters decide group/topic semantics.
func TestConsumerFactoryCreatesConsumer(t *testing.T) {
	factory := NewConsumerFactory(Config{Brokers: []string{"127.0.0.1:9092"}}, slog.New(slog.DiscardHandler))

	client, err := factory.NewConsumer("group-a", "topic-a")
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()
	require.Equal(t, time.Second, client.OptValue(kgo.FetchMaxWait),
		"a retried message waits for the next fetch, so an idle fetch must end within the shortest retry backoff")
}

type fxLifecycle struct {
	hooks []fx.Hook
}

func (l *fxLifecycle) Append(h fx.Hook) {
	l.hooks = append(l.hooks, h)
}

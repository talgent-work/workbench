package integrationmessage

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/go-jimu/components/ddd/message"
	contribkafka "github.com/go-jimu/contrib/message/kafka"
	pkgkafka "github.com/talgent-work/workbench/pkg/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	publisherAttempts = 3
	publisherBackoff  = 100 * time.Millisecond
	// consumerAttempts is how often a failed message is handled in place,
	// counting the first, before it is dropped.
	consumerAttempts = 3
	// handlerTimeout bounds one message's handlers. The consumer holds a
	// rebalance until its batch is committed, and the client gives up its
	// group session after kgo's 60s RebalanceTimeout; a stuck handler gives up
	// well within it, leaving room for the rest of its partition's batch.
	handlerTimeout = 20 * time.Second
)

func NewPublisher(client *kgo.Client) message.Publisher {
	return contribkafka.NewPublisher(
		client,
		contribkafka.WithPublishRetry(publisherAttempts, publishBackoff),
	)
}

// NewConsumer returns the consumer of topics for one consumer group. A handler
// failure is handled again in place, holding up its partition behind it;
// decode and unhandled failures, a failure marked contribkafka.NonRetryable,
// and one that used its attempts are dropped and logged at Error.
func NewConsumer(
	cfg Config,
	factory *pkgkafka.ConsumerFactory,
	resolver message.PayloadResolver,
	logger *slog.Logger,
	topics IntegrationTopics,
) (*contribkafka.Consumer, error) {
	client, err := factory.NewConsumer(cfg.ConsumerGroup, topics.Strings()...)
	if err != nil {
		return nil, fmt.Errorf("integration message consumer: %w", err)
	}
	consumer, err := contribkafka.NewConsumer(
		client,
		contribkafka.RetryPolicy(contribkafka.RetryConfig{
			MaxAttempts: consumerAttempts,
			OnExhausted: contribkafka.FailureActionDrop,
		}),
		contribkafka.WithPayloadResolver(resolver),
		contribkafka.WithHandlerTimeout(handlerTimeout),
		contribkafka.WithLogger(logger),
		contribkafka.WithCloseClient(true),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("integration message consumer: %w", err)
	}
	return consumer, nil
}

func publishBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	return time.Duration(attempt) * publisherBackoff
}

type IntegrationTopics []message.Kind

// Strings names each topic once: contexts in one process may consume the same
// fact, each with its own handler.
func (t IntegrationTopics) Strings() []string {
	topics := make([]string, 0, len(t))
	seen := make(map[message.Kind]bool, len(t))
	for _, kind := range t {
		if kind == "" || seen[kind] {
			continue
		}
		seen[kind] = true
		topics = append(topics, string(kind))
	}
	return topics
}

// Package messagebus publishes and consumes Integration Messages on Kafka
// through the go-jimu Kafka adapter. Bounded contexts depend on
// message.Publisher and message.Handler only; brokers, consumer groups,
// topics, retries and the consumers' lifecycle stay here.
//
// A process lists PublisherModule when it publishes. A module that consumes
// builds its own Consumer with NewConsumer, under its own consumer group so
// that its progress never depends on another module's, and provides it under
// ConsumerTag; pkg.Lifecycle starts and stops every Consumer.
package messagebus

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-jimu/components/ddd/message"
	jimukafka "github.com/go-jimu/contrib/message/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
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
	// clientLogLevel keeps franz-go's lifecycle logs, such as group joins,
	// partition assignments and broker connections, without logging every
	// request.
	clientLogLevel = kgo.LogLevelInfo
	// consumerFetchMaxWait bounds how long the broker holds a fetch that has
	// no records. A partition the consumer resumes after a retry backoff is
	// fetched only by the next fetch, so on an idle topic every retry waits at
	// least this long; kgo's 5s default would stretch the consumer's 1s and 2s
	// backoffs to 5s each.
	consumerFetchMaxWait = time.Second
)

// Option locates the Kafka brokers.
type Option struct {
	Brokers []string `json:"brokers" toml:"brokers" yaml:"brokers"`
}

// Validate requires at least one broker.
func (o Option) Validate() error {
	for _, broker := range o.Brokers {
		if strings.TrimSpace(broker) != "" {
			return nil
		}
	}
	return errors.New("messagebus: at least one broker is required")
}

// PublisherModule provides the process's message.Publisher.
var PublisherModule = fx.Module("internal.pkg.messagebus.publisher",
	fx.Provide(NewPublisher),
)

// NewPublisher publishes through one producer client, acknowledged by every
// in-sync replica. Publish is synchronous, so the client holds nothing to
// flush and stays open until the process exits.
func NewPublisher(option Option, logger *slog.Logger) (message.Publisher, error) {
	if err := option.Validate(); err != nil {
		return nil, err
	}
	client, err := jimukafka.NewClient(
		kgo.SeedBrokers(option.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.AllowAutoTopicCreation(),
		kgo.WithLogger(jimukafka.KgoLogger(logger, clientLogLevel)),
	)
	if err != nil {
		return nil, fmt.Errorf("messagebus publisher: %w", err)
	}
	return jimukafka.NewPublisher(client, jimukafka.WithPublishRetry(publisherAttempts, func(attempt int) time.Duration {
		return time.Duration(max(attempt, 1)) * publisherBackoff
	})), nil
}

// NewConsumer consumes, in group, the topics its handlers listen to and
// subscribes them. A handler failure is handled again in place, holding up
// its partition behind it; decode and unhandled failures, a failure marked
// message.ErrPermanent, and one that used its attempts are dropped and logged
// at Error. Payloads resolve by their protobuf full name, which is the kind.
func NewConsumer(option Option, group string, shutdowner fx.Shutdowner, logger *slog.Logger, handlers ...message.Handler) (*Consumer, error) {
	if err := option.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(group) == "" {
		return nil, errors.New("messagebus: a consumer group is required")
	}
	var topics Topics
	for _, handler := range handlers {
		topics = append(topics, handler.Listening()...)
	}
	client, err := jimukafka.NewClient(
		kgo.SeedBrokers(option.Brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics.Strings()...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(consumerFetchMaxWait),
		kgo.AllowAutoTopicCreation(),
		kgo.WithLogger(jimukafka.KgoLogger(logger, clientLogLevel)),
	)
	if err != nil {
		return nil, fmt.Errorf("messagebus consumer %s: %w", group, err)
	}
	runner, err := jimukafka.NewConsumer(client,
		jimukafka.RetryPolicy(jimukafka.RetryConfig{MaxAttempts: consumerAttempts, OnExhausted: jimukafka.FailureActionDrop}),
		jimukafka.WithPayloadResolver(message.PayloadResolverFunc(protoPayload)),
		jimukafka.WithHandlerTimeout(handlerTimeout),
		jimukafka.WithLogger(logger),
		jimukafka.WithCloseClient(true),
	)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("messagebus consumer %s: %w", group, err)
	}
	for _, handler := range handlers {
		if err := runner.Subscribe(handler); err != nil {
			runner.Close()
			return nil, fmt.Errorf("messagebus consumer %s: %w", group, err)
		}
	}
	return newConsumer(runner, shutdowner, logger.With(slog.String("consumer_group", group))), nil
}

// Topics are the kinds of the facts a consumer reads; a kind's topic is the
// kind itself.
type Topics []message.Kind

// Strings names each topic once: several handlers of one consumer may listen
// to the same fact.
func (t Topics) Strings() []string {
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

func protoPayload(kind message.Kind) (proto.Message, error) {
	typ, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName(kind))
	if err != nil {
		return nil, fmt.Errorf("integration message payload %s: %w", kind, err)
	}
	return typ.New().Interface(), nil
}

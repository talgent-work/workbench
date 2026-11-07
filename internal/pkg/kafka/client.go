// Package kafka owns shared Kafka client initialization.
package kafka

import (
	"context"
	"log/slog"
	"time"

	contribkafka "github.com/go-jimu/contrib/message/kafka"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/fx"
)

type Config struct {
	Brokers []string `json:"brokers" toml:"brokers" yaml:"brokers"`
}

// clientLogLevel keeps franz-go's lifecycle logs, such as group joins,
// partition assignments, and broker connections, without logging every
// request.
const clientLogLevel = kgo.LogLevelInfo

// consumerFetchMaxWait bounds how long the broker holds a fetch that has no
// records. A partition the consumer resumes after a retry backoff is fetched
// only by the next fetch, so on an idle topic every retry waits at least this
// long; kgo's 5s default would stretch the consumer's 1s and 2s backoffs to
// 5s each.
const consumerFetchMaxWait = time.Second

func NewProducer(lc fx.Lifecycle, cfg Config, logger *slog.Logger) (*kgo.Client, error) {
	client, err := contribkafka.NewClient(
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerBatchCompression(kgo.SnappyCompression()),
		kgo.AllowAutoTopicCreation(),
		kgo.WithLogger(contribkafka.KgoLogger(logger, clientLogLevel)),
	)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		client.Close()
		return nil
	}})
	return client, nil
}

type ConsumerFactory struct {
	cfg    Config
	logger *slog.Logger
}

func NewConsumerFactory(cfg Config, logger *slog.Logger) *ConsumerFactory {
	return &ConsumerFactory{cfg: cfg, logger: logger}
}

// NewConsumer returns a client for one consumer group. The consumer commits
// what it handled and holds rebalances until its batch is committed, so the
// client leaves both to it.
func (f *ConsumerFactory) NewConsumer(group string, topics ...string) (*kgo.Client, error) {
	return contribkafka.NewClient(
		kgo.SeedBrokers(f.cfg.Brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		kgo.BlockRebalanceOnPoll(),
		kgo.FetchMaxWait(consumerFetchMaxWait),
		kgo.AllowAutoTopicCreation(),
		kgo.WithLogger(contribkafka.KgoLogger(f.logger, clientLogLevel)),
	)
}

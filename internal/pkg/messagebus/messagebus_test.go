package messagebus

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-jimu/components/ddd/message"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"
)

func TestOptionYAMLUnmarshal(t *testing.T) {
	var option Option
	require.NoError(t, yaml.Unmarshal([]byte("brokers:\n  - 127.0.0.1:9092\n"), &option))
	require.Equal(t, []string{"127.0.0.1:9092"}, option.Brokers)
	require.NoError(t, option.Validate())
	require.Error(t, Option{Brokers: []string{" "}}.Validate())
}

// Several handlers of one consumer may listen to the same fact; the consumer
// reads its topic once.
func TestTopicsNameEachKindOnce(t *testing.T) {
	require.Equal(t, []string{"a.v1.Fact", "b.v1.Fact"}, Topics{"a.v1.Fact", "", "b.v1.Fact", "a.v1.Fact"}.Strings())
}

// The adapter refuses at construction a client that commits on its own or
// rebalances while a batch is still being handled, and one without a payload
// resolver; NewConsumer builds one it accepts and subscribes its handlers.
func TestNewConsumerSubscribesItsHandlers(t *testing.T) {
	discard := slog.New(slog.DiscardHandler)
	consumer, err := NewConsumer(Option{Brokers: []string{"127.0.0.1:9092"}}, "group-a", &recordingShutdowner{}, discard, listening{"a.v1.Fact"})
	require.NoError(t, err)
	consumer.runner.Close()

	_, err = NewConsumer(Option{Brokers: []string{"127.0.0.1:9092"}}, " ", &recordingShutdowner{}, discard, listening{"a.v1.Fact"})
	require.Error(t, err, "a consumer needs its own group")
}

// A consumer that stops on its own cannot go on: the process shuts down with
// a failure exit code and its restart consumes again from the last commit.
func TestConsumerStoppingOnItsOwnShutsTheProcessDown(t *testing.T) {
	runner := &fakeRunner{stop: make(chan error, 1)}
	shutdowner := &recordingShutdowner{}
	consumer := newConsumer(runner, shutdowner, slog.New(slog.DiscardHandler))
	require.NoError(t, consumer.Start(context.Background()))

	runner.stop <- errors.New("commit failed")
	require.Eventually(t, func() bool { return shutdowner.calls.Load() == 1 }, time.Second, 10*time.Millisecond)
	require.True(t, runner.closed.Load(), "the client leaves its group")
	require.NoError(t, consumer.Stop(context.Background()))
}

// Stop drains the consumer gracefully, so running handlers finish instead of
// being canceled, and waits for its run to end without shutting the process
// down a second time.
func TestConsumerStopDrainsGracefully(t *testing.T) {
	runner := &fakeRunner{stop: make(chan error, 1)}
	shutdowner := &recordingShutdowner{}
	consumer := newConsumer(runner, shutdowner, slog.New(slog.DiscardHandler))
	require.NoError(t, consumer.Start(context.Background()))

	require.NoError(t, consumer.Stop(context.Background()))
	require.True(t, runner.shutdown.Load())
	require.False(t, runner.canceled.Load(), "a graceful stop does not cancel the run")
	require.True(t, runner.closed.Load())
	require.Zero(t, shutdowner.calls.Load())
}

type listening []message.Kind

func (l listening) Listening() []message.Kind                   { return l }
func (listening) Handle(context.Context, message.Message) error { return nil }

type fakeRunner struct {
	stop     chan error
	shutdown atomic.Bool
	canceled atomic.Bool
	closed   atomic.Bool
}

func (r *fakeRunner) Run(ctx context.Context) error {
	select {
	case err := <-r.stop:
		return err
	case <-ctx.Done():
		r.canceled.Store(true)
		return nil
	}
}

func (r *fakeRunner) Shutdown(context.Context) error {
	r.shutdown.Store(true)
	r.stop <- nil
	return nil
}

func (r *fakeRunner) Close() { r.closed.Store(true) }

type recordingShutdowner struct{ calls atomic.Int32 }

func (s *recordingShutdowner) Shutdown(...fx.ShutdownOption) error {
	s.calls.Add(1)
	return nil
}

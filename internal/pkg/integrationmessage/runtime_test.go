package integrationmessage

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
)

type blockingConsumer struct {
	started chan struct{}
	stopped chan struct{}
	closed  bool
}

func (c *blockingConsumer) Run(ctx context.Context) error {
	close(c.started)
	<-ctx.Done()
	close(c.stopped)
	return nil
}

func (c *blockingConsumer) Close() { c.closed = true }

type refusedShutdowner struct{ t *testing.T }

func (s refusedShutdowner) Shutdown(...fx.ShutdownOption) error {
	s.t.Error("a consumer stopped by the application shut the application down")
	return nil
}

// Intent (real): a process that provides the integration consumer runs it for
// its lifetime, so subscriptions registered by business modules are consumed
// (a removed module once took controlplane's only runner start with it), and
// stopping the application ends consumption and releases the consumer's
// client before shutdown completes.
func TestRunWithLifecycleConsumesForTheApplicationLifetime(t *testing.T) {
	lc := fxtest.NewLifecycle(t)
	consumer := &blockingConsumer{started: make(chan struct{}), stopped: make(chan struct{})}
	RunWithLifecycle(lc, consumer, refusedShutdowner{t: t}, slog.New(slog.DiscardHandler))

	lc.RequireStart()
	select {
	case <-consumer.started:
	case <-time.After(time.Second):
		t.Fatal("consumer did not run after the application started")
	}
	lc.RequireStop()
	select {
	case <-consumer.stopped:
	default:
		t.Fatal("application stopped while the consumer was still running")
	}
	require.True(t, consumer.closed, "the consumer's client is released once it stops")
}

type stoppingConsumer struct {
	err    error
	closed chan struct{}
}

func (c *stoppingConsumer) Run(context.Context) error { return c.err }

func (c *stoppingConsumer) Close() { close(c.closed) }

// Intent: a consumer that stops on its own cannot run again on its client, so
// the process shuts down with a failure exit code and its restart consumes
// again, instead of living on without consuming.
func TestRunWithLifecycleShutsDownWhenTheConsumerStopsOnItsOwn(t *testing.T) {
	for name, runErr := range map[string]error{
		"with an error":  errors.New("kafka commit failed"),
		"without reason": nil,
	} {
		t.Run(name, func(t *testing.T) {
			consumer := &stoppingConsumer{err: runErr, closed: make(chan struct{})}
			app := fxtest.New(t, fx.Invoke(func(lc fx.Lifecycle, shutdowner fx.Shutdowner) {
				RunWithLifecycle(lc, consumer, shutdowner, slog.New(slog.DiscardHandler))
			}))
			app.RequireStart()
			select {
			case shutdown := <-app.Wait():
				require.Equal(t, 1, shutdown.ExitCode)
			case <-time.After(time.Second):
				t.Fatal("the application kept running after its consumer stopped")
			}
			app.RequireStop()
			select {
			case <-consumer.closed:
			default:
				t.Fatal("the stopped consumer's client was not released")
			}
		})
	}
}

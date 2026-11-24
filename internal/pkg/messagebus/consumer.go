package messagebus

import (
	"context"
	"log/slog"
	"sync"

	"github.com/go-jimu/components/sloghelper"
	"go.uber.org/fx"
)

// ConsumerTag collects the process's consumers for pkg.Lifecycle; a module
// provides its Consumer under it.
const ConsumerTag = `group:"messagebus.consumers"`

// stoppedExitCode is the process exit code after a consumer stopped on its
// own.
const stoppedExitCode = 1

// consumerRunner is the adapter's consumer: a run and its graceful shutdown.
type consumerRunner interface {
	Run(ctx context.Context) error
	Shutdown(ctx context.Context) error
	Close()
}

// Consumer is one consumer group's run, an entry point of the process.
type Consumer struct {
	runner     consumerRunner
	shutdowner fx.Shutdowner
	logger     *slog.Logger

	mu       sync.Mutex
	stopping bool
	cancel   context.CancelFunc
	done     chan struct{}
}

func newConsumer(runner consumerRunner, shutdowner fx.Shutdowner, logger *slog.Logger) *Consumer {
	return &Consumer{runner: runner, shutdowner: shutdowner, logger: logger}
}

// Start runs the consumer for the process lifetime. A client serves a single
// run, so a consumer that stops on its own, such as after a failed commit,
// shuts the process down with a failure exit code; its restart consumes again
// from the last commit.
func (c *Consumer) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.mu.Lock()
	c.cancel, c.done = cancel, done
	c.mu.Unlock()
	go func() {
		defer close(done)
		err := c.runner.Run(ctx)
		c.runner.Close()
		c.mu.Lock()
		stopping := c.stopping
		c.mu.Unlock()
		if stopping {
			return
		}
		attrs := []slog.Attr{}
		if err != nil {
			attrs = append(attrs, sloghelper.Error(err))
		}
		c.logger.LogAttrs(ctx, slog.LevelError, "integration message consumer stopped; shutting down", attrs...)
		if err := c.shutdowner.Shutdown(fx.ExitCode(stoppedExitCode)); err != nil {
			c.logger.LogAttrs(ctx, slog.LevelError, "integration message consumer could not shut the process down", sloghelper.Error(err))
		}
	}()
	return nil
}

// Stop lets running handlers finish and commit, then waits for the run to
// end; once ctx ends the stop is forced and running handlers are canceled.
func (c *Consumer) Stop(ctx context.Context) error {
	c.mu.Lock()
	c.stopping = true
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	defer cancel()
	if err := c.runner.Shutdown(ctx); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

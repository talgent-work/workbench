package integrationmessage

import (
	"context"
	"log/slog"

	"github.com/go-jimu/components/ddd/message"
	"github.com/go-jimu/components/sloghelper"
	"go.uber.org/fx"
)

// stoppedExitCode is the process exit code when a consumer stops on its own.
const stoppedExitCode = 1

// Consumer is a broker consumer run for the application's lifetime: Run
// consumes until its ctx ends or it cannot go on, and Close then releases its
// client, which leaves its consumer group.
type Consumer interface {
	message.Runner
	Close()
}

// RunWithLifecycle runs the consumer for the application's lifetime. It is
// owned by the module that provides the consumer, so a process that consumes
// integration messages always runs it, whichever business modules subscribe.
// Subscriptions are registered while the graph is built, before OnStart.
//
// A consumer stops on its own only when it cannot go on, such as after a
// failed commit, and its client serves a single run. The process then shuts
// down with a failure exit code, and its restart consumes again from the last
// commit.
func RunWithLifecycle(lc fx.Lifecycle, consumer Consumer, shutdowner fx.Shutdowner, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	var cancel context.CancelFunc
	done := make(chan struct{})
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			go func() {
				err := consumer.Run(ctx)
				consumer.Close()
				close(done)
				if ctx.Err() != nil {
					return
				}
				attrs := []slog.Attr{}
				if err != nil {
					attrs = append(attrs, sloghelper.Error(err))
				}
				logger.LogAttrs(ctx, slog.LevelError, "integration message consumer stopped; shutting down", attrs...)
				if err := shutdowner.Shutdown(fx.ExitCode(stoppedExitCode)); err != nil {
					logger.LogAttrs(ctx, slog.LevelError, "integration message consumer could not shut down the process", sloghelper.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel == nil {
				return nil
			}
			cancel()
			select {
			case <-done:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
}

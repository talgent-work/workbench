// Package lifecyclelog reports how a process stops. Shutdown order follows the
// order hooks were registered, which the module graph decides implicitly; the
// log makes that order and each hook's duration observable.
package lifecyclelog

import (
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

// Option replaces fx's own event log, keeping only shutdown and failures.
var Option = fx.WithLogger(func(logger *slog.Logger) fxevent.Logger { return stopLogger{logger: logger} })

type stopLogger struct{ logger *slog.Logger }

func (l stopLogger) LogEvent(event fxevent.Event) {
	switch e := event.(type) {
	case *fxevent.Stopping:
		l.logger.Info("process stopping", slog.String("signal", e.Signal.String()))
	case *fxevent.OnStopExecuted:
		attrs := []any{slog.String("hook", e.FunctionName), slog.String("registered_by", e.CallerName), slog.Duration("runtime", e.Runtime)}
		if e.Err != nil {
			l.logger.Warn("stop hook failed", append(attrs, slog.Any("error", e.Err))...)
			return
		}
		l.logger.Info("stop hook done", attrs...)
	case *fxevent.Started:
		// Also carries a dependency graph that failed to assemble, which
		// otherwise exits the process without a word.
		if e.Err != nil {
			l.logger.Error("start failed", slog.Any("error", e.Err))
		}
	case *fxevent.OnStartExecuted:
		if e.Err != nil {
			l.logger.Error("start hook failed", slog.String("hook", e.FunctionName), slog.String("registered_by", e.CallerName), slog.Any("error", e.Err))
		}
	}
}

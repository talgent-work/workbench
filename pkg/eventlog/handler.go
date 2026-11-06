package eventlog

import (
	"context"
	"log/slog"
	"time"

	domainevent "github.com/go-jimu/components/ddd/event"
)

// Handler wraps a domain event handler with execution-boundary completion logs.
func Handler(logger *slog.Logger, next domainevent.Handler) domainevent.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return handler{logger: logger, next: next}
}

type handler struct {
	logger *slog.Logger
	next   domainevent.Handler
}

func (h handler) Listening() []domainevent.Kind {
	return h.next.Listening()
}

func (h handler) Handle(ctx context.Context, ev domainevent.Event) {
	start := time.Now()
	defer func() {
		if recovered := recover(); recovered != nil {
			h.logger.ErrorContext(ctx, "domain event handler completed",
				slog.String("operation", "domain_event.handle"),
				slog.String("outcome", "failed"),
				slog.String("event_kind", string(ev.Kind())),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Any("panic", recovered),
			)
			panic(recovered)
		}
		h.logger.InfoContext(ctx, "domain event handler completed",
			slog.String("operation", "domain_event.handle"),
			slog.String("outcome", "success"),
			slog.String("event_kind", string(ev.Kind())),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	}()
	h.next.Handle(ctx, ev)
}

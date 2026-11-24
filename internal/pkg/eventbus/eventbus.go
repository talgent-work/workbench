// Package eventbus builds the in-memory Domain Event dispatcher each bounded
// context keeps for its own events, and the execution log of its handlers.
// Each context closes its dispatcher on stop after pkg.Lifecycle has stopped
// the entry points that raise its events.
package eventbus

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"github.com/go-jimu/components/ddd/event"
)

// handlerTimeout bounds one handler call.
const handlerTimeout = 30 * time.Second

// NewDispatcher returns a context's dispatcher. Events with no subscriber are
// expected, such as a fact only another context reacts to, and logged at Debug.
func NewDispatcher(logger *slog.Logger) *event.InMemoryDispatcher {
	return event.NewDispatcher(
		event.WithLogger(logger),
		event.WithHandlerTimeout(handlerTimeout),
		event.WithUnhandledEventHandler(func(unhandled event.UnhandledContext) {
			logger.Debug("domain event has no subscriber", slog.String("event_kind", string(unhandled.Event.Kind())))
		}),
	)
}

// Logging logs each completed Handle at Debug, as the go-jimu components log
// a unit of work that succeeded. A panic propagates to the dispatcher, which
// recovers and reports it.
func Logging(logger *slog.Logger, next event.Handler) event.Handler {
	return logging{logger: logger, next: next, name: reflect.TypeOf(next).String()}
}

type logging struct {
	logger *slog.Logger
	next   event.Handler
	name   string
}

func (h logging) Listening() []event.Kind { return h.next.Listening() }

func (h logging) Handle(ctx context.Context, ev event.Event) {
	start := time.Now()
	h.next.Handle(ctx, ev)
	h.logger.LogAttrs(ctx, slog.LevelDebug, "domain event handled",
		slog.String("event_kind", string(ev.Kind())),
		slog.String("event_handler", h.name),
		slog.Int64("elapsed_ms", time.Since(start).Milliseconds()))
}

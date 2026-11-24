package eventbus

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/go-jimu/components/ddd/event"
	"github.com/stretchr/testify/require"
)

type workClosed struct{}

func (workClosed) Kind() event.Kind { return "agent.work_closed" }

type recordingHandler struct {
	mu      sync.Mutex
	handled []event.Kind
	panics  bool
}

func (h *recordingHandler) Listening() []event.Kind {
	return []event.Kind{workClosed{}.Kind()}
}

func (h *recordingHandler) Handle(_ context.Context, ev event.Event) {
	if h.panics {
		panic("boom")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handled = append(h.handled, ev.Kind())
}

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.handled)
}

// A subscribed handler runs once per event, its completion logged at Debug
// with the event kind and the time it took.
func TestDispatcherRunsLoggedHandlers(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dispatcher := NewDispatcher(logger)
	handler := &recordingHandler{}
	dispatcher.Subscribe(Logging(logger, handler))

	require.NoError(t, dispatcher.Dispatch(context.Background(), workClosed{}))
	require.NoError(t, dispatcher.Close(context.Background()))
	require.Equal(t, 1, handler.count())
	require.Contains(t, logs.String(), "level=DEBUG msg=\"domain event handled\"")
	require.Contains(t, logs.String(), "event_kind=agent.work_closed")
	require.Contains(t, logs.String(), "elapsed_ms=")
}

// A panicking handler reaches the dispatcher, which recovers and reports it.
func TestLoggingLeavesPanicsToTheDispatcher(t *testing.T) {
	logs := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logs, nil))
	dispatcher := NewDispatcher(logger)
	dispatcher.Subscribe(Logging(logger, &recordingHandler{panics: true}))

	require.NoError(t, dispatcher.Dispatch(context.Background(), workClosed{}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, dispatcher.Close(ctx))
	require.Contains(t, logs.String(), "domain event handler panicked")
	require.NotContains(t, logs.String(), "domain event handled")
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

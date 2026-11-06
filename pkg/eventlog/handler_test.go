package eventlog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	domainevent "github.com/go-jimu/components/ddd/event"
)

const testEventKind domainevent.Kind = "test.event"

type testEvent struct{}

func (testEvent) Kind() domainevent.Kind { return testEventKind }

type testHandler struct{}

func (testHandler) Listening() []domainevent.Kind             { return []domainevent.Kind{testEventKind} }
func (testHandler) Handle(context.Context, domainevent.Event) {}

// Intent (real): domain event handlers must emit one completion log because
// asynchronous event handling has no synchronous caller to observe success.
func TestHandlerLogsCompletion(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	handler := Handler(logger, testHandler{})

	handler.Handle(context.Background(), testEvent{})

	entry := lastEventLogEntry(t, &buf)
	if entry["operation"] != "domain_event.handle" {
		t.Fatalf("operation = %v, want domain_event.handle", entry["operation"])
	}
	if entry["outcome"] != "success" {
		t.Fatalf("outcome = %v, want success", entry["outcome"])
	}
	if entry["event_kind"] != string(testEventKind) {
		t.Fatalf("event_kind = %v, want %s", entry["event_kind"], testEventKind)
	}
	if _, ok := entry["duration_ms"]; !ok {
		t.Fatalf("duration_ms missing from log entry: %#v", entry)
	}
}

func lastEventLogEntry(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) == 0 || len(lines[0]) == 0 {
		t.Fatal("no log entries captured")
	}
	var entry map[string]any
	if err := json.Unmarshal(lines[len(lines)-1], &entry); err != nil {
		t.Fatalf("unmarshal log entry: %v; line=%s", err, lines[len(lines)-1])
	}
	return entry
}

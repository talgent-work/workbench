package messagedelivery

import (
	"context"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"

	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
)

// Delivery diagnostics are non-durable live evidence and must use their
// separate diagnostic subject so Portal live consumers can distinguish them.
func TestDiagnosticNATSPublisher_PublishesProtoJSONToDiagnosticSubject(t *testing.T) {
	srv := runTestNATSServer(t)
	nc := mustTestNATSConn(t, srv)
	publisher := NewDiagnosticNATSPublisher(&staticConnector{nc: nc})

	msgCh := make(chan *natsgo.Msg, 1)
	sub, err := nc.ChanSubscribe(diagnosticSubject("work-1"), msgCh)
	if err != nil {
		t.Fatalf("ChanSubscribe: %v", err)
	}
	t.Cleanup(func() { _ = sub.Unsubscribe() })
	if err := nc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	occurredAt := time.Date(2026, 5, 20, 1, 2, 3, 0, time.UTC)
	if err := publisher.PublishDiagnostic(context.Background(), Diagnostic{
		AgentWorkID: "work-1",
		Severity:    "error",
		Reason:      "storage_unavailable",
		Message:     "UnifiedMessage delivery failed",
		OccurredAt:  occurredAt,
	}); err != nil {
		t.Fatalf("PublishDiagnostic: %v", err)
	}

	var raw *natsgo.Msg
	select {
	case raw = <-msgCh:
	case <-time.After(time.Second):
		t.Fatal("diagnostic was not published")
	}
	if raw.Subject != "session.work-1.diagnostic" {
		t.Fatalf("subject = %q, want session.work-1.diagnostic", raw.Subject)
	}
	if !strings.Contains(string(raw.Data), `"agent_work_id"`) {
		t.Fatalf("payload JSON = %s, want proto field name agent_work_id", string(raw.Data))
	}
	if strings.Contains(string(raw.Data), `"agentWorkId"`) {
		t.Fatalf("payload JSON = %s, want no camelCase agentWorkId field", string(raw.Data))
	}
	if !strings.Contains(string(raw.Data), `"occurred_at"`) {
		t.Fatalf("payload JSON = %s, want proto field name occurred_at", string(raw.Data))
	}

	var got streamgatewaypublicv1.StreamDiagnosticFrame
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(raw.Data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GetAgentWorkId() != "work-1" || got.GetReason() != "storage_unavailable" {
		t.Fatalf("diagnostic = %+v", &got)
	}
	if got.GetOccurredAt() == nil || !got.GetOccurredAt().AsTime().Equal(occurredAt) {
		t.Fatalf("occurred_at = %v, want %v", got.GetOccurredAt(), occurredAt)
	}
}

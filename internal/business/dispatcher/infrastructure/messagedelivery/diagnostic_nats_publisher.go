package messagedelivery

import (
	"context"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"

	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
)

var diagnosticProtoJSON = protojson.MarshalOptions{UseProtoNames: true}

// Diagnostic is a live-only condition told to people reading a Work, using
// Dispatcher-owned status vocabulary. It is never stored.
type Diagnostic struct {
	AgentWorkID string
	Severity    string
	Reason      string
	Message     string
	OccurredAt  time.Time
}

type DiagnosticNATSPublisher struct {
	conn natsConnector
}

var _ DiagnosticPublisher = (*DiagnosticNATSPublisher)(nil)

func NewDiagnosticNATSPublisher(conn natsConnector) *DiagnosticNATSPublisher {
	return &DiagnosticNATSPublisher{conn: conn}
}

func (p *DiagnosticNATSPublisher) PublishDiagnostic(_ context.Context, diagnostic Diagnostic) error {
	occurredAt := diagnostic.OccurredAt
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	frame := &streamgatewaypublicv1.StreamDiagnosticFrame{
		AgentWorkId: diagnostic.AgentWorkID,
		Severity:    diagnostic.Severity,
		Reason:      diagnostic.Reason,
		Message:     diagnostic.Message,
		OccurredAt:  timestamppb.New(occurredAt),
	}
	raw, err := diagnosticProtoJSON.Marshal(frame)
	if err != nil {
		return err
	}
	nc, err := p.conn.Conn()
	if err != nil {
		return err
	}
	return nc.Publish(diagnosticSubject(diagnostic.AgentWorkID), raw)
}

func diagnosticSubject(workID string) string {
	return "session." + workID + ".diagnostic"
}

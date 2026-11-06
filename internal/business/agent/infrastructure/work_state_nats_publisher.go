package infrastructure

import (
	"context"

	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"google.golang.org/protobuf/proto"
)

// WorkStateNATSPublisher sends a Work's state frame to its stream readers.
type WorkStateNATSPublisher struct{ conn *pkgnats.LazyConn }

func NewWorkStateNATSPublisher(conn *pkgnats.LazyConn) *WorkStateNATSPublisher {
	return &WorkStateNATSPublisher{conn: conn}
}
func (p *WorkStateNATSPublisher) PublishWorkState(_ context.Context, frame *streamgatewaypublicv1.WorkStateFrame) error {
	nc, err := p.conn.Conn()
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return nc.Publish("session_state."+frame.GetAgentWorkId(), payload)
}

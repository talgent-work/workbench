package messagedelivery

import (
	"context"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

var liveMessageProtoJSON = protojson.MarshalOptions{UseProtoNames: true}

type natsConnector interface {
	Conn() (*natsgo.Conn, error)
}

// LiveNATSPublisher tells live readers of a Work each UnifiedMessage. The
// detail body stays with storage: readers fetch it on demand.
type LiveNATSPublisher struct {
	conn natsConnector
}

var _ LivePublisher = (*LiveNATSPublisher)(nil)

func NewLiveNATSPublisher(conn natsConnector) *LiveNATSPublisher {
	return &LiveNATSPublisher{conn: conn}
}

func (p *LiveNATSPublisher) PublishLive(_ context.Context, msg *dispatcherpublicv1.UnifiedMessage) error {
	live := msg
	// Bodies go to storage only; readers fetch them through GetToolDetail.
	if msg.GetDetail() != "" || msg.GetToolPreview() != nil {
		live = proto.CloneOf(msg)
		live.Detail, live.ToolPreview = "", nil
	}
	raw, err := liveMessageProtoJSON.Marshal(live)
	if err != nil {
		return err
	}
	nc, err := p.conn.Conn()
	if err != nil {
		return err
	}
	return nc.Publish(liveMessageSubject(msg.GetAgentWorkId()), raw)
}

func liveMessageSubject(workID string) string {
	return "session." + workID
}

package infrastructure

import (
	"context"
	"errors"
	"slices"

	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"google.golang.org/protobuf/proto"
)

// LiveStreamPublisher sends a Work's live frames on the NATS subjects
// StreamGateway reads: its state frame to its stream readers, and, on the
// Work's own access subject, which open readers lost access to its message
// stream.
type LiveStreamPublisher struct {
	conn *pkgnats.LazyConn
	// workState is StreamGateway's frame of a Work's state, the same one a
	// reader's admission answers with.
	workState func(query.WorkView) *streamgatewaypublicv1.WorkStateFrame
}

var _ eventhandler.LiveStreamPort = (*LiveStreamPublisher)(nil)

func NewLiveStreamPublisher(conn *pkgnats.LazyConn, workState func(query.WorkView) *streamgatewaypublicv1.WorkStateFrame) (*LiveStreamPublisher, error) {
	if conn == nil || workState == nil {
		return nil, errors.New("agent: a NATS connection and the Work state frame are required for live streams")
	}
	return &LiveStreamPublisher{conn: conn, workState: workState}, nil
}

func (p *LiveStreamPublisher) PublishWorkState(_ context.Context, state query.WorkView) error {
	frame := p.workState(state)
	return p.publish("session_state."+frame.GetAgentWorkId(), frame)
}

func (p *LiveStreamPublisher) PublishWorkAccessWithdrawal(_ context.Context, withdrawal eventhandler.WorkAccessWithdrawal) error {
	frame := &streamgatewaypublicv1.WorkAccessFrame{AgentWorkId: withdrawal.WorkID}
	if withdrawal.MadePrivate {
		frame.Change = &streamgatewaypublicv1.WorkAccessFrame_MadePrivate{MadePrivate: &streamgatewaypublicv1.RetainedWorkReaders{ReaderIds: slices.Clone(withdrawal.RetainedReaderIDs)}}
	} else {
		frame.Change = &streamgatewaypublicv1.WorkAccessFrame_RevokedReaderId{RevokedReaderId: withdrawal.RevokedReaderID}
	}
	return p.publish("session_access."+withdrawal.WorkID, frame)
}

func (p *LiveStreamPublisher) publish(subject string, frame proto.Message) error {
	nc, err := p.conn.Conn()
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return nc.Publish(subject, payload)
}

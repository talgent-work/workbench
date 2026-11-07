package infrastructure

import (
	"context"
	"slices"

	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/eventhandler"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
	"google.golang.org/protobuf/proto"
)

// WorkAccessNATSPublisher tells Streamgateway, on the Work's own live subject,
// which open readers lost access to its message stream.
type WorkAccessNATSPublisher struct{ conn *pkgnats.LazyConn }

var _ eventhandler.WorkAccessWithdrawalPort = (*WorkAccessNATSPublisher)(nil)

func NewWorkAccessNATSPublisher(conn *pkgnats.LazyConn) *WorkAccessNATSPublisher {
	return &WorkAccessNATSPublisher{conn: conn}
}

func (p *WorkAccessNATSPublisher) PublishWorkAccessWithdrawal(_ context.Context, withdrawal eventhandler.WorkAccessWithdrawal) error {
	nc, err := p.conn.Conn()
	if err != nil {
		return err
	}
	frame := &streamgatewaypublicv1.WorkAccessFrame{AgentWorkId: withdrawal.WorkID}
	if withdrawal.MadePrivate {
		frame.Change = &streamgatewaypublicv1.WorkAccessFrame_MadePrivate{MadePrivate: &streamgatewaypublicv1.RetainedWorkReaders{ReaderIds: slices.Clone(withdrawal.RetainedReaderIDs)}}
	} else {
		frame.Change = &streamgatewaypublicv1.WorkAccessFrame_RevokedReaderId{RevokedReaderId: withdrawal.RevokedReaderID}
	}
	payload, err := proto.Marshal(frame)
	if err != nil {
		return err
	}
	return nc.Publish("session_access."+withdrawal.WorkID, payload)
}

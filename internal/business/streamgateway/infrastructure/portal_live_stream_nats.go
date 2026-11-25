package infrastructure

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
)

var portalLiveProtoJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

// errNotConnected refuses a subscription while the broker is out of reach; the
// reader retries once the connection is back.
var errNotConnected = errors.New("NATS is not connected")

// subscribeConfirmTimeout bounds waiting for the broker to confirm a
// subscription when the caller set no earlier deadline.
const subscribeConfirmTimeout = 5 * time.Second

type natsConnector interface {
	Conn() (*natsgo.Conn, error)
}

// PortalLiveStreamNATS reads a Work's live frames through one work.<id>.>
// subscription on the process's shared connection.
type PortalLiveStreamNATS struct {
	connector natsConnector
	logger    *slog.Logger
}

var _ query.PortalLiveStream = (*PortalLiveStreamNATS)(nil)

func NewPortalLiveStreamNATS(conn *pkgnats.LazyConn, logger *slog.Logger) query.PortalLiveStream {
	return newPortalLiveStreamNATS(conn, logger)
}

func newPortalLiveStreamNATS(connector natsConnector, logger *slog.Logger) *PortalLiveStreamNATS {
	if logger == nil {
		logger = slog.Default()
	}
	return &PortalLiveStreamNATS{connector: connector, logger: logger.With("component", "streamgateway.portal_live_stream_nats")}
}

// Subscribe registers the subscription and waits, within ctx, until the broker
// has it, so a frame published after Subscribe returns reaches deliver. A lost
// connection keeps the subscription: the client library restores it, and the
// readers hear that live frames were interrupted.
func (f *PortalLiveStreamNATS) Subscribe(ctx context.Context, workID string, deliver func(query.PortalLivePayload)) (query.PortalLiveSubscription, error) {
	if !pkgnats.ValidWorkID(workID) {
		return nil, query.ErrInvalidWorkID
	}
	nc, err := f.connector.Conn()
	if err != nil {
		return nil, err
	}
	if !nc.IsConnected() {
		return nil, errNotConnected
	}
	sub := &natsSubscription{
		workID: workID, nc: nc, deliver: deliver,
		ended: make(chan struct{}), closed: make(chan struct{}),
		logger: f.logger.With("agent_work_id", workID),
	}
	sub.status = nc.StatusChanged(natsgo.RECONNECTING, natsgo.CONNECTED, natsgo.CLOSED)
	sub.frames, err = nc.Subscribe(pkgnats.WorkFrames(workID), sub.receive)
	if err != nil {
		nc.RemoveStatusListener(sub.status)
		return nil, err
	}
	confirming, cancel := context.WithTimeout(ctx, subscribeConfirmTimeout)
	defer cancel()
	if err := nc.FlushWithContext(confirming); err != nil {
		_ = sub.Close()
		return nil, err
	}
	go sub.watch()
	return sub, nil
}

type natsSubscription struct {
	workID  string
	nc      *natsgo.Conn
	frames  *natsgo.Subscription
	status  chan natsgo.Status
	deliver func(query.PortalLivePayload)
	logger  *slog.Logger

	ended, closed      chan struct{}
	endOnce, closeOnce sync.Once
}

var _ query.PortalLiveSubscription = (*natsSubscription)(nil)

func (s *natsSubscription) Ended() <-chan struct{} { return s.ended }

func (s *natsSubscription) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.closed)
		s.nc.RemoveStatusListener(s.status)
		if s.frames != nil {
			err = s.frames.Unsubscribe()
		}
		if errors.Is(err, natsgo.ErrConnectionClosed) || errors.Is(err, natsgo.ErrBadSubscription) {
			err = nil
		}
	})
	return err
}

// watch tells readers when live frames stop arriving and ends the
// subscription once its connection is closed for good.
func (s *natsSubscription) watch() {
	for {
		select {
		case <-s.closed:
			return
		case status, ok := <-s.status:
			if !ok {
				return
			}
			switch status {
			case natsgo.RECONNECTING:
				now := time.Now().UTC()
				s.deliver(&query.DiagnosticPayload{Frame: &query.Diagnostic{
					AgentWorkID: s.workID, Severity: "error", Reason: "live_delivery_unavailable",
					Message: "Live updates were interrupted; reload the conversation to see what was missed.", OccurredAt: &now,
				}})
			case natsgo.CLOSED:
				s.endOnce.Do(func() { close(s.ended) })
				return
			}
		}
	}
}

func (s *natsSubscription) receive(m *natsgo.Msg) {
	kind, ok := pkgnats.WorkFrameKind(s.workID, m.Subject)
	if !ok {
		return
	}
	payload, err := decodeFrame(kind, m.Data)
	if err != nil {
		s.logger.Warn("undecodable live frame", slog.String("kind", kind), slog.Any("error", err))
		return
	}
	if payload != nil {
		s.deliver(payload)
	}
}

func decodeFrame(kind string, data []byte) (query.PortalLivePayload, error) {
	switch kind {
	case pkgnats.WorkMessageFrames:
		var msg dispatcherpublicv1.UnifiedMessage
		if err := portalLiveProtoJSONUnmarshal.Unmarshal(data, &msg); err != nil {
			return nil, err
		}
		return &query.UnifiedMessagePayload{Message: MessageFromProto(&msg)}, nil
	case pkgnats.WorkStateFrames:
		var frame streamgatewaypublicv1.WorkStateFrame
		if err := proto.Unmarshal(data, &frame); err != nil {
			return nil, err
		}
		return &query.WorkStatePayload{Frame: workStateFromProto(&frame)}, nil
	case pkgnats.WorkAccessFrames:
		var frame streamgatewaypublicv1.WorkAccessFrame
		if err := proto.Unmarshal(data, &frame); err != nil {
			return nil, err
		}
		return workAccessFromProto(&frame), nil
	case pkgnats.WorkDiagnosticFrames:
		var frame streamgatewaypublicv1.StreamDiagnosticFrame
		if err := portalLiveProtoJSONUnmarshal.Unmarshal(data, &frame); err != nil {
			return nil, err
		}
		return &query.DiagnosticPayload{Frame: diagnosticFromProto(&frame)}, nil
	default:
		return nil, nil
	}
}

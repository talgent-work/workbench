package infrastructure

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"

	natsgo "github.com/nats-io/nats.go"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	streamgatewaypublicv1 "github.com/talgent-work/workbench/gen/go/talgent/streamgateway/public/v1"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	pkgnats "github.com/talgent-work/workbench/internal/pkg/nats"
)

var portalLiveProtoJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

type natsConnector interface {
	Conn() (*natsgo.Conn, error)
}

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
	return &PortalLiveStreamNATS{
		connector: connector,
		logger:    logger.With("component", "streamgateway.portal_live_stream_nats"),
	}
}

func (f *PortalLiveStreamNATS) SubscribeSession(ctx context.Context, sessionID string) (query.PortalLiveSubscription, error) {
	nc, err := f.connector.Conn()
	if err != nil {
		return nil, err
	}
	sub := &portalLiveNATSSubscription{
		ch:      make(chan query.PortalLivePayload, 256),
		nc:      nc,
		done:    make(chan struct{}),
		recvErr: context.Canceled,
		logger:  f.logger.With("agent_work_id", sessionID),
	}
	msgSub, err := nc.Subscribe(runtimeMessageSubject(sessionID), func(nm *natsgo.Msg) {
		var msg dispatcherpublicv1.UnifiedMessage
		if err := portalLiveProtoJSONUnmarshal.Unmarshal(nm.Data, &msg); err != nil {
			return
		}
		sub.enqueue(&query.UnifiedMessagePayload{Message: MessageFromProto(&msg)})
	})
	if err != nil {
		return nil, err
	}
	sub.subs = append(sub.subs, msgSub)

	workSub, err := nc.Subscribe(workStateSubject(sessionID), func(nm *natsgo.Msg) {
		var frame streamgatewaypublicv1.WorkStateFrame
		if err := proto.Unmarshal(nm.Data, &frame); err != nil {
			return
		}
		sub.enqueue(&query.WorkStatePayload{Frame: workStateFromProto(&frame)})
	})
	if err != nil {
		sub.logger.WarnContext(ctx, "work state live subscribe failed", slog.Any("error", err))
	} else {
		sub.subs = append(sub.subs, workSub)
	}

	accessSub, err := nc.Subscribe(workAccessSubject(sessionID), func(nm *natsgo.Msg) {
		var frame streamgatewaypublicv1.WorkAccessFrame
		if err := proto.Unmarshal(nm.Data, &frame); err != nil {
			return
		}
		sub.enqueue(workAccessFromProto(&frame))
	})
	if err != nil {
		// Without it, a withdrawal cannot end open streams immediately.
		_ = sub.shutdown(err, true)
		return nil, err
	}
	sub.subs = append(sub.subs, accessSub)

	diagnosticSub, err := nc.Subscribe(diagnosticSubject(sessionID), func(nm *natsgo.Msg) {
		var frame streamgatewaypublicv1.StreamDiagnosticFrame
		if err := portalLiveProtoJSONUnmarshal.Unmarshal(nm.Data, &frame); err != nil {
			return
		}
		sub.enqueue(&query.DiagnosticPayload{Frame: diagnosticFromProto(&frame)})
	})
	if err != nil {
		sub.logger.WarnContext(ctx, "diagnostic live subscribe failed", slog.Any("error", err))
	} else {
		sub.subs = append(sub.subs, diagnosticSub)
	}

	if err := nc.Flush(); err != nil {
		_ = sub.shutdown(err, true)
		return nil, err
	}
	sub.statusC = nc.StatusChanged(natsgo.DISCONNECTED, natsgo.CLOSED)
	switch nc.Status() {
	case natsgo.DISCONNECTED:
		sub.shutdown(natsgo.ErrDisconnected, false) //nolint:errcheck
	case natsgo.CLOSED:
		sub.shutdown(natsgo.ErrConnectionClosed, false) //nolint:errcheck
	}
	go sub.watchConn()
	return sub, nil
}

type portalLiveNATSSubscription struct {
	ch      chan query.PortalLivePayload
	nc      *natsgo.Conn
	subs    []*natsgo.Subscription
	once    sync.Once
	done    chan struct{}
	closing atomic.Bool
	errMu   sync.RWMutex
	recvErr error
	statusC chan natsgo.Status
	logger  *slog.Logger
}

var _ query.PortalLiveSubscription = (*portalLiveNATSSubscription)(nil)

func (s *portalLiveNATSSubscription) Recv(ctx context.Context) (query.PortalLivePayload, error) {
	if s.closing.Load() {
		return nil, s.terminalErr()
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, s.terminalErr()
	case payload := <-s.ch:
		if s.closing.Load() {
			return nil, s.terminalErr()
		}
		return payload, nil
	}
}

func (s *portalLiveNATSSubscription) Close() error {
	return s.shutdown(context.Canceled, true)
}

func (s *portalLiveNATSSubscription) enqueue(payload query.PortalLivePayload) {
	if s.closing.Load() {
		return
	}
	select {
	case s.ch <- payload:
	case <-s.done:
	default:
		s.logger.Warn("dropping portal live payload because subscription buffer is full")
	}
}

func (s *portalLiveNATSSubscription) watchConn() {
	select {
	case <-s.done:
	case status, ok := <-s.statusC:
		if !ok {
			return
		}
		switch status {
		case natsgo.DISCONNECTED:
			s.shutdown(natsgo.ErrDisconnected, false) //nolint:errcheck
		case natsgo.CLOSED:
			s.shutdown(natsgo.ErrConnectionClosed, false) //nolint:errcheck
		}
	}
}

func (s *portalLiveNATSSubscription) shutdown(recvErr error, unsubscribe bool) error {
	var err error
	s.once.Do(func() {
		s.closing.Store(true)
		s.setTerminalErr(recvErr)
		if s.statusC != nil {
			s.nc.RemoveStatusListener(s.statusC)
		}
		close(s.done)
		if unsubscribe {
			for _, sub := range s.subs {
				if subErr := sub.Unsubscribe(); err == nil {
					err = subErr
				}
			}
		}
	})
	return err
}

func (s *portalLiveNATSSubscription) setTerminalErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	s.recvErr = err
}

func (s *portalLiveNATSSubscription) terminalErr() error {
	s.errMu.RLock()
	defer s.errMu.RUnlock()
	return s.recvErr
}

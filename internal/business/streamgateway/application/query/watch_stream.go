package query

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"slices"
	"time"
)

type WatchStreamRequest struct {
	IncludeUnattributedErrors bool
	ReaderID                  string
	Admission                 ReaderAdmission
	Agent                     *string
	Kinds                     []string
	TurnIDs                   []string
	AgentWorkID               string
}

var ErrLiveUnavailable = errors.New("live stream unavailable")

// ErrLiveSubscriptionClosed means the Work's live subscription ended on this
// process; the reader opens a new stream.
var ErrLiveSubscriptionClosed = errors.New("live subscription closed")

// WatchStream streams a Work's live frames to one admitted reader. It replays
// nothing: the reader reads stored history apart and merges by message ID.
type WatchStream struct {
	access   StreamAccessPort
	registry *HubRegistry
	gate     *streamGate
	logger   *slog.Logger
	options  WatchStreamOptions
}

func NewWatchStream(stream PortalLiveStream, drops LiveDropRecorder, logger *slog.Logger, access StreamAccessPort, options WatchStreamOptions) *WatchStream {
	if logger == nil {
		logger = slog.Default()
	}
	options = options.withDefaults()
	return &WatchStream{
		access:   access,
		registry: NewHubRegistry(stream, drops, logger),
		gate:     newStreamGate(options),
		logger:   logger.With("component", "streamgateway.watch_stream"),
		options:  options,
	}
}

func (w *WatchStream) Run(ctx context.Context, req WatchStreamRequest, send func(PortalLivePayload) error) error {
	workID := req.AgentWorkID
	if workID == "" {
		return ErrSessionIDRequired
	}
	leave, err := w.gate.enter(req.ReaderID)
	if err != nil {
		return err
	}
	defer leave()
	// Subscribe before admitting, so an access withdrawal committed after the
	// admission is always observed by this stream.
	reader, err := w.registry.Acquire(ctx, workID, req.ReaderID)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrInvalidWorkID) {
			return err
		}
		return errors.Join(ErrLiveUnavailable, err)
	}
	defer reader.Close()
	starting, err := w.access.AdmitStreamReader(ctx, req.ReaderID, req.Admission, workID)
	if err != nil {
		return err
	}
	// The first frame tells the reader its stream is listening, so history it
	// reads from now on covers what came before: the Work as it stands, or a
	// heartbeat when Agent supplies no state.
	var first PortalLivePayload = &HeartbeatPayload{}
	if starting != nil {
		first = &WorkStatePayload{Frame: starting}
	}
	if err := send(first); err != nil {
		return err
	}

	watching, stop := context.WithCancelCause(ctx)
	defer stop(nil)
	go w.readmit(watching, stop, req, workID)
	err = w.forward(watching, reader, newMessageScope(req), send)
	if ctx.Err() == nil && errors.Is(context.Cause(watching), ErrWorkAccessWithdrawn) {
		err = ErrWorkAccessWithdrawn
	}
	if errors.Is(err, ErrWorkAccessWithdrawn) {
		// The forwarding has stopped, so this is the stream's only sender.
		if sendErr := send(&AccessWithdrawnPayload{AgentWorkID: workID}); sendErr != nil && ctx.Err() == nil {
			return sendErr
		}
	}
	// These terminal conditions complete the stream without an RPC error.
	// Record the reason here; middleware owns request completion.
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		w.logger.InfoContext(ctx, "watch stream stopped by caller", slog.String("agent_work_id", workID), slog.String("cause", "client_context"))
		return nil
	case errors.Is(err, ErrWorkAccessWithdrawn):
		w.logger.InfoContext(ctx, "watch stream ended by access withdrawal", slog.String("agent_work_id", workID), slog.String("cause", "access_withdrawn"))
		return nil
	case errors.Is(err, ErrLiveSubscriptionClosed):
		w.logger.WarnContext(ctx, "watch stream live subscription closed", slog.String("agent_work_id", workID), slog.String("cause", "live_subscription_closed"))
		return nil
	}
	return err
}

// forward sends the reader's frames until the stream ends, and a heartbeat
// whenever a whole interval passes without a frame.
func (w *WatchStream) forward(ctx context.Context, reader *LiveReader, scope messageScope, send func(PortalLivePayload) error) error {
	heartbeat := time.NewTimer(w.options.HeartbeatInterval)
	defer heartbeat.Stop()
	deliver := func(payload PortalLivePayload) error {
		heartbeat.Reset(w.options.HeartbeatInterval)
		return send(payload)
	}
	for {
		// A withdrawal ends the stream before anything still queued is sent.
		select {
		case <-reader.revoked:
			return ErrWorkAccessWithdrawn
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-reader.revoked:
			return ErrWorkAccessWithdrawn
		case <-reader.ended:
			return ErrLiveSubscriptionClosed
		case state := <-reader.state:
			if err := deliver(state); err != nil {
				return err
			}
		case payload := <-reader.data:
			if message, ok := payload.(*UnifiedMessagePayload); ok && !scope.includes(message.Message) {
				continue
			}
			if err := deliver(payload); err != nil {
				return err
			}
		case <-heartbeat.C:
			if err := deliver(&HeartbeatPayload{}); err != nil {
				return err
			}
		}
	}
}

// readmit asks Agent again about every readmission interval, jittered so
// streams opened together do not ask together, and stops the stream once the
// reader is no longer admitted; an unavailable Agent leaves the stream open.
func (w *WatchStream) readmit(ctx context.Context, stop context.CancelCauseFunc, req WatchStreamRequest, workID string) {
	for {
		timer := time.NewTimer(jittered(w.options.ReadmissionInterval))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		err := w.access.AuthorizeRead(ctx, req.ReaderID, req.Admission, workID)
		if errors.Is(err, ErrWorkAccessDenied) {
			stop(ErrWorkAccessWithdrawn)
			return
		}
		if err != nil && ctx.Err() == nil {
			w.logger.WarnContext(ctx, "watch stream readmission unavailable", slog.String("agent_work_id", workID), slog.Any("error", err))
		}
	}
}

// jittered spreads interval by a fifth either way.
func jittered(interval time.Duration) time.Duration {
	spread := int64(interval) / 5
	if spread <= 0 {
		return interval
	}
	return interval - time.Duration(spread) + time.Duration(rand.Int64N(2*spread))
}

// messageScope is the part of a Work's messages a reader asked for, the same
// Agent, kind and turn scope as its history.
type messageScope struct {
	workID                    string
	includeUnattributedErrors bool
	agent                     *string
	kinds                     []string
	turnIDs                   []string
}

func newMessageScope(req WatchStreamRequest) messageScope {
	return messageScope{workID: req.AgentWorkID, includeUnattributedErrors: req.IncludeUnattributedErrors, agent: req.Agent,
		kinds: normalizeMessageFilters(req.Kinds), turnIDs: normalizeMessageFilters(req.TurnIDs)}
}

func (s messageScope) includes(m *Message) bool {
	if m == nil || m.AgentWorkID != s.workID {
		return false
	}
	unattributedError := s.includeUnattributedErrors && m.Agent == nil && m.Kind == "UNIFIED_MESSAGE_KIND_ERROR"
	if s.agent != nil && !unattributedError && (m.Agent == nil || *m.Agent != *s.agent) {
		return false
	}
	if len(s.kinds) > 0 && !slices.Contains(s.kinds, m.Kind) {
		return false
	}
	return len(s.turnIDs) == 0 || slices.Contains(s.turnIDs, m.TurnID)
}

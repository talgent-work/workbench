package query

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// liveChanBuffer caps per-subscriber live buffering before the hub starts
// dropping payloads for that subscriber. The slow subscriber's Portal
// connection must reconnect to catch up via replay.
const liveChanBuffer = 256

// HubRegistry is the process-wide map of agent_work_id -> sessionHub.
// It is safe for concurrent Acquire/Release calls across goroutines.
type HubRegistry struct {
	mu     sync.Mutex
	hubs   map[string]*sessionHub
	stream PortalLiveStream
	logger *slog.Logger
}

// NewHubRegistry constructs the registry.
func NewHubRegistry(stream PortalLiveStream, logger *slog.Logger) *HubRegistry {
	return &HubRegistry{
		hubs:   map[string]*sessionHub{},
		stream: stream,
		logger: logger.With("component", "streamgateway.hub"),
	}
}

// Subscription is the handle the registry hands back to a caller.
// Live returns the receive-only live channel; the caller does not close it.
type Subscription struct {
	hub  *sessionHub
	live chan PortalLivePayload
}

func (s *Subscription) Live() <-chan PortalLivePayload { return s.live }

// Acquire returns a hub handle plus a Subscription. Callers must pair every
// Acquire with exactly one Release.
func (r *HubRegistry) Acquire(ctx context.Context, sessionID string) (*sessionHub, *Subscription, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.hubs[sessionID]; ok {
		if sub, ok := existing.addSubscriber(); ok {
			return existing, sub, nil
		}
		delete(r.hubs, sessionID)
	}

	streamSub, err := r.stream.SubscribeSession(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}
	var hub *sessionHub
	hub = newSessionHub(sessionID, streamSub, r.logger, func() {
		r.mu.Lock()
		if cur, ok := r.hubs[sessionID]; ok && cur == hub {
			delete(r.hubs, sessionID)
		}
		r.mu.Unlock()
	})
	r.hubs[sessionID] = hub
	hub.startBroadcast()
	sub, _ := hub.addSubscriber()
	return hub, sub, nil
}

// Release removes the subscription from its hub. If no subscribers remain,
// the hub closes its upstream subscription and evicts itself from the registry.
func (r *HubRegistry) Release(hub *sessionHub, sub *Subscription) {
	hub.removeSubscriber(sub)
}

type sessionHub struct {
	sessionID string
	sub       PortalLiveSubscription
	logger    *slog.Logger
	onStop    func()

	mu       sync.Mutex
	subs     map[*Subscription]struct{}
	refcount int
	closed   bool
	stopped  bool
}

func newSessionHub(sessionID string, sub PortalLiveSubscription, logger *slog.Logger, onStop func()) *sessionHub {
	return &sessionHub{
		sessionID: sessionID,
		sub:       sub,
		logger:    logger.With("agent_work_id", sessionID),
		onStop:    onStop,
		subs:      map[*Subscription]struct{}{},
	}
}

func (h *sessionHub) startBroadcast() {
	go h.broadcastLoop()
}

func (h *sessionHub) broadcastLoop() {
	for {
		payload, err := h.sub.Recv(context.Background())
		if err != nil {
			h.stop("upstream_subscription_closed", err)
			return
		}
		h.fanout(payload)
	}
}

func (h *sessionHub) fanout(payload PortalLivePayload) {
	h.mu.Lock()
	snapshot := make([]*Subscription, 0, len(h.subs))
	for s := range h.subs {
		snapshot = append(snapshot, s)
	}
	h.mu.Unlock()
	for _, s := range snapshot {
		select {
		case s.live <- payload:
		default:
			attrs := []any{}
			if msgPayload, ok := payload.(*UnifiedMessagePayload); ok && msgPayload.Message != nil {
				attrs = append(attrs, slog.String("message_id", msgPayload.Message.ID))
			}
			h.logger.Warn("dropping live payload for slow subscriber", attrs...)
		}
	}
}

func (h *sessionHub) addSubscriber() (*Subscription, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, false
	}
	sub := &Subscription{hub: h, live: make(chan PortalLivePayload, liveChanBuffer)}
	h.subs[sub] = struct{}{}
	h.refcount++
	return sub, true
}

func (h *sessionHub) removeSubscriber(sub *Subscription) {
	h.mu.Lock()
	if _, ok := h.subs[sub]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.subs, sub)
	h.refcount--
	shouldClose := h.refcount == 0 && !h.closed
	if shouldClose {
		h.closed = true
	}
	h.mu.Unlock()

	if shouldClose {
		h.stop("last_subscriber", nil)
		_ = h.sub.Close()
	}
}

func (h *sessionHub) stop(cause string, err error) {
	h.mu.Lock()
	if h.stopped {
		h.mu.Unlock()
		return
	}
	h.stopped = true
	h.closed = true
	snapshot := make([]*Subscription, 0, len(h.subs))
	for sub := range h.subs {
		snapshot = append(snapshot, sub)
	}
	h.mu.Unlock()

	for _, sub := range snapshot {
		close(sub.live)
	}

	attrs := []slog.Attr{
		slog.String("cause", cause),
		slog.Int("subscriber_count", len(snapshot)),
	}
	if err != nil {
		attrs = append(attrs, slog.Any("error", err))
	}
	if errors.Is(err, context.Canceled) || cause == "last_subscriber" {
		h.logger.LogAttrs(context.Background(), slog.LevelInfo, "session hub live subscription closed", attrs...)
	} else {
		h.logger.LogAttrs(context.Background(), slog.LevelWarn, "session hub live subscription closed", attrs...)
	}
	if h.onStop != nil {
		h.onStop()
	}
}

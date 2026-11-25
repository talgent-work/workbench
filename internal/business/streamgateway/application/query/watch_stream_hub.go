package query

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// subscribeTimeout bounds subscribing to a Work's frames. The subscription is
// shared by every reader of the Work on this process, so it does not end with
// the request of the reader that happened to start it.
const subscribeTimeout = 5 * time.Second

// LiveDropRecorder counts the live frames readers lost because they fell
// behind, by the kind of frame lost.
type LiveDropRecorder interface {
	Dropped(kind string)
}

// HubRegistry shares one live subscription among the readers of each Work on
// this process. Its lock covers only its map: subscribing happens outside it,
// so one Work's slow subscription never delays readers of another Work.
type HubRegistry struct {
	mu     sync.Mutex
	hubs   map[string]*hubEntry
	stream PortalLiveStream
	drops  LiveDropRecorder
	logger *slog.Logger
}

// hubEntry is a Work's hub, or the placeholder its first reader holds while
// subscribing. readers counts the readers holding the entry, under the
// registry lock; hub and err are set once, before ready closes.
type hubEntry struct {
	ready   chan struct{}
	hub     *workHub
	err     error
	readers int
}

func NewHubRegistry(stream PortalLiveStream, drops LiveDropRecorder, logger *slog.Logger) *HubRegistry {
	return &HubRegistry{
		hubs:   map[string]*hubEntry{},
		stream: stream,
		drops:  drops,
		logger: logger.With("component", "streamgateway.hub"),
	}
}

// Acquire attaches a reader to the Work's live frames, subscribing only when no
// reader on this process watches the Work yet. Readers arriving while that
// subscription is made wait for it under their own context and share its
// outcome. Every LiveReader it returns must be closed.
func (r *HubRegistry) Acquire(ctx context.Context, workID, readerID string) (*LiveReader, error) {
	r.mu.Lock()
	entry, building := r.hubs[workID], false
	if entry == nil {
		entry, building = &hubEntry{ready: make(chan struct{})}, true
		r.hubs[workID] = entry
	}
	entry.readers++
	r.mu.Unlock()

	if building {
		r.build(ctx, workID, entry)
	}
	select {
	case <-entry.ready:
	case <-ctx.Done():
		r.release(workID, entry)
		return nil, ctx.Err()
	}
	if entry.err != nil {
		r.release(workID, entry)
		return nil, entry.err
	}
	reader := newLiveReader(readerID, r.drops, r.logger.With("agent_work_id", workID))
	entry.hub.attach(reader)
	reader.release = func() {
		entry.hub.detach(reader)
		r.release(workID, entry)
	}
	return reader, nil
}

func (r *HubRegistry) build(ctx context.Context, workID string, entry *hubEntry) {
	subscribing, cancel := context.WithTimeout(context.WithoutCancel(ctx), subscribeTimeout)
	defer cancel()
	hub := &workHub{readers: map[*LiveReader]struct{}{}, done: make(chan struct{})}
	sub, err := r.stream.Subscribe(subscribing, workID, hub.deliver)
	if err != nil {
		// Readers that come later subscribe again; those waiting share the failure.
		r.forget(workID, entry)
		entry.err = err
		close(entry.ready)
		return
	}
	hub.sub = sub
	entry.hub = hub
	close(entry.ready)
	go func() {
		select {
		case <-sub.Ended():
		case <-hub.done:
			return
		}
		// The connection closed for good: later readers subscribe afresh, and
		// current ones end so their clients reconnect.
		r.forget(workID, entry)
		hub.end()
		r.logger.Warn("live subscription ended", slog.String("agent_work_id", workID), slog.String("cause", "connection_closed"))
	}()
}

// release drops one reader's hold on entry; the last one closes its hub.
func (r *HubRegistry) release(workID string, entry *hubEntry) {
	r.mu.Lock()
	entry.readers--
	last := entry.readers == 0
	if last && r.hubs[workID] == entry {
		delete(r.hubs, workID)
	}
	r.mu.Unlock()
	if last && entry.hub != nil {
		entry.hub.close()
	}
}

func (r *HubRegistry) forget(workID string, entry *hubEntry) {
	r.mu.Lock()
	if r.hubs[workID] == entry {
		delete(r.hubs, workID)
	}
	r.mu.Unlock()
}

// workHub fans one Work's live frames out to its readers without waiting for
// any of them.
type workHub struct {
	sub       PortalLiveSubscription
	mu        sync.Mutex
	readers   map[*LiveReader]struct{}
	ended     bool
	closeOnce sync.Once
	// done closes when the hub's last reader leaves.
	done chan struct{}
}

func (h *workHub) deliver(payload PortalLivePayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for reader := range h.readers {
		reader.offer(payload)
	}
}

func (h *workHub) attach(reader *LiveReader) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ended {
		reader.end()
		return
	}
	h.readers[reader] = struct{}{}
}

func (h *workHub) detach(reader *LiveReader) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.readers, reader)
}

// end ends every current and later reader of a hub whose subscription ended.
func (h *workHub) end() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ended = true
	for reader := range h.readers {
		reader.end()
	}
}

func (h *workHub) close() {
	h.closeOnce.Do(func() {
		close(h.done)
		_ = h.sub.Close()
	})
}

// LiveReader is one reader's view of a Work's live frames. Messages and
// notices queue up to liveReaderBuffer and are dropped beyond it; the Work's
// state keeps only its latest frame; an access withdrawal never waits behind
// either.
type LiveReader struct {
	id      string
	drops   LiveDropRecorder
	logger  *slog.Logger
	data    chan PortalLivePayload
	state   chan *WorkStatePayload
	revoked chan struct{}
	ended   chan struct{}
	release func()

	revokeOnce, endOnce, closeOnce, behindOnce sync.Once
}

// liveReaderBuffer caps the messages and notices waiting for one reader. A
// reader this far behind loses the newest frames until it catches up: live
// delivery is best-effort, and its history read covers what it missed.
const liveReaderBuffer = 256

func newLiveReader(id string, drops LiveDropRecorder, logger *slog.Logger) *LiveReader {
	return &LiveReader{
		id: id, drops: drops, logger: logger,
		data:    make(chan PortalLivePayload, liveReaderBuffer),
		state:   make(chan *WorkStatePayload, 1),
		revoked: make(chan struct{}),
		ended:   make(chan struct{}),
	}
}

// offer is called by one hub delivery at a time and never blocks.
func (r *LiveReader) offer(payload PortalLivePayload) {
	switch p := payload.(type) {
	case *WorkAccessPayload:
		if p.Withdraws(r.id) {
			r.revokeOnce.Do(func() { close(r.revoked) })
		}
	case *WorkStatePayload:
		select {
		case r.state <- p:
		default:
			select {
			case <-r.state:
			default:
			}
			r.state <- p
		}
	default:
		select {
		case r.data <- payload:
		default:
			r.drops.Dropped(frameKind(payload))
			r.behindOnce.Do(func() { r.logger.Info("live reader fell behind; dropping its newest frames") })
		}
	}
}

func (r *LiveReader) end() { r.endOnce.Do(func() { close(r.ended) }) }

// Close detaches the reader; the Work's last reader closes its subscription.
func (r *LiveReader) Close() { r.closeOnce.Do(r.release) }

func frameKind(payload PortalLivePayload) string {
	switch payload.(type) {
	case *UnifiedMessagePayload:
		return "message"
	case *DiagnosticPayload:
		return "diagnostic"
	default:
		return "other"
	}
}

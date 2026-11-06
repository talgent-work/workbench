package query

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// ReplaySettleDelay gives ClickHouse async_insert a short window to make
// just-written rows visible before the subscriber issues its first replay.
const ReplaySettleDelay = 300 * time.Millisecond

// ErrLiveSubscriptionClosed means the upstream live stream ended while a
// WatchStream subscriber was waiting for more payloads.
var ErrLiveSubscriptionClosed = errors.New("live subscription closed")

type replayError struct {
	err error
}

func (e *replayError) Error() string { return "replay messages: " + e.err.Error() }
func (e *replayError) Unwrap() error { return e.err }

type sendError struct {
	messageID string
	err       error
}

func (e *sendError) Error() string {
	if e.messageID == "" {
		return "send payload: " + e.err.Error()
	}
	return fmt.Sprintf("send message %s: %v", e.messageID, e.err)
}

func (e *sendError) Unwrap() error { return e.err }

// SubscriberDeps are the things a subscriber needs but does not own.
type SubscriberDeps struct {
	IncludeUnattributedErrors bool
	Agent                     *string
	Kinds                     []string
	TurnIDs                   []string
	Reader                    UnifiedMessageReader
	LiveCh                    <-chan PortalLivePayload
	SettleDelay               time.Duration
}

// Subscriber drives one WatchStream call: settle, replay, drain, live.
type Subscriber struct {
	includeUnattributedErrors bool
	agent                     *string
	kinds                     []string
	turnIDs                   []string
	reader                    UnifiedMessageReader
	live                      <-chan PortalLivePayload
	settleDelay               time.Duration
}

// NewSubscriber constructs a Subscriber.
func NewSubscriber(d SubscriberDeps) *Subscriber {
	delay := d.SettleDelay
	if delay < 0 {
		delay = 0
	}
	return &Subscriber{includeUnattributedErrors: d.IncludeUnattributedErrors, reader: d.Reader, live: d.LiveCh, settleDelay: delay, agent: d.Agent, kinds: normalizeMessageFilters(d.Kinds), turnIDs: normalizeMessageFilters(d.TurnIDs)}
}

// Run executes the replay+live pipeline until ctx is done or send returns
// an error. The zero values of afterTimestamp and afterID mean "from the
// beginning of history".
func (s *Subscriber) Run(
	ctx context.Context,
	sessionID string,
	afterTimestamp time.Time,
	afterID string,
	send func(PortalLivePayload) error,
) error {
	if s.settleDelay > 0 {
		timer := time.NewTimer(s.settleDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}

	const initialReplayLimit = 50
	const replayPageSize = 500
	// Bounded overlap suppression; content changes under an existing ID remain visible.
	const dedupWindow = 1024
	seen := make(map[string][32]byte, dedupWindow)
	order := make([]string, dedupWindow)
	next := 0
	sendPayload := func(payload PortalLivePayload) error {
		if payload == nil {
			return nil
		}
		if p, ok := payload.(*UnifiedMessagePayload); ok {
			m := p.Message
			if m == nil || m.AgentWorkID != sessionID {
				return nil
			}
			includeError := s.includeUnattributedErrors && m.Agent == nil && m.Kind == "UNIFIED_MESSAGE_KIND_ERROR"
			if s.agent != nil && !includeError && (m.Agent == nil || *m.Agent != *s.agent) {
				return nil
			}
			if len(s.kinds) > 0 && !slices.Contains(s.kinds, m.Kind) {
				return nil
			}
			if len(s.turnIDs) > 0 && !slices.Contains(s.turnIDs, m.TurnID) {
				return nil
			}
			encoded, err := json.Marshal(m)
			if err != nil {
				return err
			}
			fingerprint := sha256.Sum256(encoded)
			previous, known := seen[m.ID]
			if known && previous == fingerprint {
				return nil
			}
			if err := send(payload); err != nil {
				return &sendError{messageID: m.ID, err: err}
			}
			if !known {
				delete(seen, order[next])
				order[next] = m.ID
				next = (next + 1) % dedupWindow
			}
			seen[m.ID] = fingerprint
			return nil
		}
		if err := send(payload); err != nil {
			return &sendError{err: err}
		}
		return nil
	}
	cursor := afterTimestamp
	cursorID := afterID
	for {
		latestReplay := cursor.IsZero() && cursorID == ""
		limit := replayPageSize
		if latestReplay {
			limit = initialReplayLimit
		}
		rows, err := s.reader.ListReplay(ctx, sessionID, ReplayCursor{
			IncludeUnattributedErrors: s.includeUnattributedErrors, Agent: s.agent, Kinds: s.kinds, TurnIDs: s.turnIDs,
			After:   cursor,
			AfterID: cursorID,
			Limit:   limit,
			Latest:  latestReplay,
		})
		if err != nil {
			return &replayError{err: err}
		}
		for _, msg := range rows {
			if err := sendPayload(&UnifiedMessagePayload{Message: msg, Replay: true}); err != nil {
				return err
			}
		}
		if len(rows) > 0 {
			last := rows[len(rows)-1]
			cursor = last.Timestamp
			cursorID = last.ID
		}
		if len(rows) < limit {
			break
		}
	}

	drain := true
	for drain {
		select {
		case payload, ok := <-s.live:
			if !ok {
				return ErrLiveSubscriptionClosed
			}
			if err := sendPayload(payload); err != nil {
				return err
			}
		default:
			drain = false
		}
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload, ok := <-s.live:
			if !ok {
				return ErrLiveSubscriptionClosed
			}
			if err := sendPayload(payload); err != nil {
				return err
			}
		}
	}
}

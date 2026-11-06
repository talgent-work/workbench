package application_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
)

// Subscriber must suppress a live UnifiedMessage that was already delivered
// by replay so reconnects do not duplicate transcript entries.
func TestSubscriber_DeduplicatesReplayAndLiveByMessageID(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w", time.Unix(1, 0)),
		unifiedMessage("m2", "w", time.Unix(2, 0)),
	}}
	liveCh := make(chan query.PortalLivePayload, 8)
	// Simulate the hub delivering one overlapping and one new message:
	liveCh <- &query.UnifiedMessagePayload{Message: unifiedMessage("m2", "w", time.Unix(2, 0))}
	liveCh <- &query.UnifiedMessagePayload{Message: unifiedMessage("m3", "w", time.Unix(3, 0))}

	got := []string{}
	sub := query.NewSubscriber(query.SubscriberDeps{
		Reader:      store,
		LiveCh:      liveCh,
		SettleDelay: 0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- sub.Run(ctx, "w", time.Time{}, "", func(payload query.PortalLivePayload) error {
			msg := payload.(*query.UnifiedMessagePayload).Message
			got = append(got, msg.ID)
			if msg.ID == "m3" {
				cancel()
			}
			return nil
		})
	}()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: unexpected error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return before timeout")
	}

	want := []string{"m1", "m2", "m3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Diagnostic frames are not UnifiedMessages, so a replayed message ID must not
// suppress a later live diagnostic that happens to reference the same work.
func TestSubscriber_DiagnosticDoesNotParticipateInUnifiedMessageDedup(t *testing.T) {
	store := &fakeStore{rows: []*query.Message{
		unifiedMessage("m1", "w", time.Unix(1, 0)),
	}}
	liveCh := make(chan query.PortalLivePayload, 8)
	liveCh <- &query.DiagnosticPayload{Frame: &query.Diagnostic{
		AgentWorkID: "w",
		Severity:    "warning",
		Reason:      "m1",
		Message:     "diagnostic references replayed message id",
	}}

	got := []string{}
	sub := query.NewSubscriber(query.SubscriberDeps{
		Reader:      store,
		LiveCh:      liveCh,
		SettleDelay: 0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- sub.Run(ctx, "w", time.Time{}, "", func(payload query.PortalLivePayload) error {
			switch p := payload.(type) {
			case *query.UnifiedMessagePayload:
				got = append(got, "message:"+p.Message.ID)
			case *query.DiagnosticPayload:
				got = append(got, "diagnostic:"+p.Frame.Reason)
				cancel()
			default:
				return fmt.Errorf("unexpected payload %T", payload)
			}
			return nil
		})
	}()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("Run: unexpected error %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return before timeout")
	}

	want := []string{"message:m1", "diagnostic:m1"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// Initial replay is a page hydration path for Portal. It should fetch only the
// newest durable rows, then live updates continue through the subscription.
func TestSubscriber_InitialReplayUsesLatestFiftyRows(t *testing.T) {
	store := &fakeStore{listFn: func(_ context.Context, _ string, opts query.ReplayCursor) ([]*query.Message, error) {
		if !opts.After.IsZero() || opts.AfterID != "" {
			return nil, fmt.Errorf("initial cursor = (%v, %q), want zero", opts.After, opts.AfterID)
		}
		if !opts.Latest {
			return nil, errors.New("initial replay did not request latest rows")
		}
		if opts.Limit != 50 {
			return nil, fmt.Errorf("initial replay limit = %d, want 50", opts.Limit)
		}
		return nil, nil
	}}
	liveCh := make(chan query.PortalLivePayload)
	close(liveCh)
	sub := query.NewSubscriber(query.SubscriberDeps{Reader: store, LiveCh: liveCh, SettleDelay: 0})
	err := sub.Run(context.Background(), "w", time.Time{}, "", func(query.PortalLivePayload) error { return nil })
	if !errors.Is(err, query.ErrLiveSubscriptionClosed) {
		t.Fatalf("Run error = %v, want live subscription closed", err)
	}
}

// A store failure mid-replay must abort the stream and surface the underlying
// error, so a transient CH outage does not silently drop messages.
func TestSubscriber_ReplayErrorPropagates(t *testing.T) {
	store := &fakeStore{listFn: func(context.Context, string, query.ReplayCursor) ([]*query.Message, error) {
		return nil, errors.New("boom")
	}}
	sub := query.NewSubscriber(query.SubscriberDeps{Reader: store, LiveCh: make(chan query.PortalLivePayload), SettleDelay: 0})
	err := sub.Run(context.Background(), "w", time.Time{}, "", func(query.PortalLivePayload) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected boom error, got %v", err)
	}
}

// When a catch-up page fills completely (pageSize rows), the subscriber must
// issue a second ListByWork with the composite cursor (last.Timestamp,
// last.ID), not re-request from the original cursor — otherwise pagination
// would loop forever over identical-timestamp clusters.
func TestSubscriber_CursorAdvancesAcrossPages(t *testing.T) {
	start := time.Unix(9, 0)
	t1 := time.Unix(10, 0)
	// Build exactly pageSize (500) rows with timestamp t1 so the first page
	// fills completely — forcing the subscriber to issue a second
	// ListByWork call, which is the only way to observe cursor advancement.
	page1 := make([]*query.Message, 500)
	for i := range page1 {
		page1[i] = unifiedMessage(fmt.Sprintf("m%03d", i), "w", t1)
	}
	// Second page: one row at t2, proving the composite cursor carried
	// (t1, "m499") through.
	t2 := time.Unix(11, 0)
	page2 := []*query.Message{unifiedMessage("tail", "w", t2)}

	store := &fakeStore{}
	call := 0
	store.listFn = func(_ context.Context, _ string, opts query.ReplayCursor) ([]*query.Message, error) {
		call++
		if call == 1 {
			if !opts.After.Equal(start) || opts.AfterID != "seed" {
				return nil, fmt.Errorf("first call cursor should start from seed, got after=%v id=%q", opts.After, opts.AfterID)
			}
			if opts.Latest {
				return nil, errors.New("catch-up replay should not use latest mode")
			}
			if opts.Limit != 500 {
				return nil, fmt.Errorf("catch-up replay limit = %d, want 500", opts.Limit)
			}
			return page1, nil
		}
		if !opts.After.Equal(t1) || opts.AfterID != "m499" {
			return nil, fmt.Errorf("second call cursor=%v id=%q, want (t1, m499)", opts.After, opts.AfterID)
		}
		return page2, nil
	}

	liveCh := make(chan query.PortalLivePayload)
	close(liveCh)
	sub := query.NewSubscriber(query.SubscriberDeps{
		Reader: store, LiveCh: liveCh, SettleDelay: 0,
	})
	var count int
	var lastID string
	if err := sub.Run(context.Background(), "w", start, "seed", func(payload query.PortalLivePayload) error {
		m := payload.(*query.UnifiedMessagePayload).Message
		count++
		lastID = m.ID
		return nil
	}); err != nil && !errors.Is(err, query.ErrLiveSubscriptionClosed) {
		t.Fatalf("Run: %v", err)
	}
	if count != 501 {
		t.Fatalf("emitted %d messages, want 501", count)
	}
	if lastID != "tail" {
		t.Fatalf("last emitted id=%q, want tail", lastID)
	}
	if call != 2 {
		t.Fatalf("ListByWork called %d times, want 2", call)
	}
}

// Context cancellation is a first-class stream end reason. Returning it lets
// WatchStream log client/page disconnects separately from store/send failures.
func TestSubscriber_ContextCancellationPropagates(t *testing.T) {
	sub := query.NewSubscriber(query.SubscriberDeps{
		Reader:      &fakeStore{},
		LiveCh:      make(chan query.PortalLivePayload),
		SettleDelay: 0,
	})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- sub.Run(ctx, "w", time.Time{}, "", func(query.PortalLivePayload) error { return nil })
	}()

	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run error = %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after context cancellation")
	}
}

// A closed live channel means the upstream live subscription ended. The caller
// needs that reason so it can log the broker-side closure and let clients reconnect.
func TestSubscriber_LiveSubscriptionClosedPropagates(t *testing.T) {
	liveCh := make(chan query.PortalLivePayload)
	close(liveCh)
	sub := query.NewSubscriber(query.SubscriberDeps{
		Reader:      &fakeStore{},
		LiveCh:      liveCh,
		SettleDelay: 0,
	})

	err := sub.Run(context.Background(), "w", time.Time{}, "", func(query.PortalLivePayload) error { return nil })
	if !errors.Is(err, query.ErrLiveSubscriptionClosed) {
		t.Fatalf("Run error = %v, want ErrLiveSubscriptionClosed", err)
	}
}

// Result-only subscribers must receive the same scope during replay and live,
// while a changed message with the same identity must still reach the browser.
func TestSubscriberFiltersReplayAndLiveWithoutSuppressingUpdates(t *testing.T) {
	root := ""
	child := "native-child"
	makeResult := func(id string, agent *string, turn string) *query.Message {
		m := unifiedMessage(id, "work", time.Unix(1, 0))
		m.Kind = "UNIFIED_MESSAGE_KIND_RESULT"
		m.Agent, m.TurnID = agent, turn
		m.Text = nil
		m.Result = &query.TurnResult{Text: "Initial"}
		return m
	}
	initial := makeResult("result", &root, "turn")
	updated := makeResult("result", &root, "turn")
	updated.Result.Text = "Updated"
	store := &fakeStore{listFn: func(_ context.Context, _ string, cursor query.ReplayCursor) ([]*query.Message, error) {
		if cursor.Agent == nil || *cursor.Agent != "" || len(cursor.Kinds) != 1 || len(cursor.TurnIDs) != 1 {
			t.Errorf("replay lost filters: %+v", cursor)
		}
		return []*query.Message{initial}, nil
	}}
	live := make(chan query.PortalLivePayload, 8)
	for _, m := range []*query.Message{initial, makeResult("child", &child, "turn"), makeResult("unknown", nil, "turn"), makeResult("other-turn", &root, "other"), updated} {
		live <- &query.UnifiedMessagePayload{Message: m}
	}
	close(live)
	subscriber := query.NewSubscriber(query.SubscriberDeps{Reader: store, LiveCh: live, Agent: &root, Kinds: []string{"UNIFIED_MESSAGE_KIND_RESULT"}, TurnIDs: []string{"turn"}})
	var contents []string
	err := subscriber.Run(context.Background(), "work", time.Time{}, "", func(payload query.PortalLivePayload) error {
		contents = append(contents, payload.(*query.UnifiedMessagePayload).Message.Result.Text)
		return nil
	})
	if !errors.Is(err, query.ErrLiveSubscriptionClosed) {
		t.Fatalf("unexpected terminal error: %v", err)
	}
	if len(contents) != 2 || contents[0] != "Initial" || contents[1] != "Updated" {
		t.Fatalf("got %v; want initial and same-ID update only", contents)
	}
}

// Work may include unattributed Harness failures without treating them as root
// messages or widening the independent kind/turn filters used by other views.
func TestSubscriberUnattributedErrorsOptIn(t *testing.T) {
	root, child := "", "native-child"
	for _, tc := range []struct {
		name               string
		include            bool
		kinds, turns, want []string
	}{
		{name: "root only", want: []string{"root"}},
		{name: "work errors", include: true, want: []string{"root", "unknown"}},
		{name: "result only", include: true, kinds: []string{"UNIFIED_MESSAGE_KIND_RESULT"}},
		{name: "turn remains constrained", include: true, turns: []string{"turn"}, want: []string{"root"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := make(chan query.PortalLivePayload, 4)
			for _, row := range []struct {
				id    string
				agent *string
				turn  string
				kind  string
			}{
				{"root", &root, "turn", "UNIFIED_MESSAGE_KIND_ERROR"},
				{"child", &child, "turn", "UNIFIED_MESSAGE_KIND_ERROR"},
				{"unknown", nil, "", "UNIFIED_MESSAGE_KIND_ERROR"},
				{"unknown-result", nil, "", "UNIFIED_MESSAGE_KIND_RESULT"},
			} {
				m := unifiedMessage(row.id, "work", time.Unix(1, 0))
				m.Agent, m.TurnID, m.Kind = row.agent, row.turn, row.kind
				live <- &query.UnifiedMessagePayload{Message: m}
			}
			close(live)
			store := &fakeStore{listFn: func(_ context.Context, _ string, cursor query.ReplayCursor) ([]*query.Message, error) {
				if cursor.IncludeUnattributedErrors != tc.include {
					t.Fatal("replay lost error inclusion")
				}
				return nil, nil
			}}
			sub := query.NewSubscriber(query.SubscriberDeps{Reader: store, LiveCh: live, Agent: &root, Kinds: tc.kinds, TurnIDs: tc.turns, IncludeUnattributedErrors: tc.include})
			var got []string
			err := sub.Run(context.Background(), "work", time.Time{}, "", func(payload query.PortalLivePayload) error {
				m := payload.(*query.UnifiedMessagePayload).Message
				if m.ID == "unknown" && m.Agent != nil {
					t.Fatal("unknown error was attributed to root")
				}
				got = append(got, m.ID)
				return nil
			})
			if !errors.Is(err, query.ErrLiveSubscriptionClosed) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tc.want, got) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

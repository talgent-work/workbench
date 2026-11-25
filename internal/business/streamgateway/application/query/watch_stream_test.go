package query

import (
	"testing"
	"time"
)

// A reader receives the same Agent, kind and turn scope live as it reads in
// history, and never another Work's messages.
func TestMessageScopeMatchesTheHistoryScope(t *testing.T) {
	root, sub := "root", "sub"
	message := func(work string, agent *string, kind, turn string) *Message {
		return &Message{AgentWorkID: work, Agent: agent, Kind: kind, TurnID: turn}
	}
	for _, tc := range []struct {
		name    string
		request WatchStreamRequest
		message *Message
		want    bool
	}{
		{"another Work", WatchStreamRequest{AgentWorkID: "w1"}, message("w2", nil, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), false},
		{"no filter", WatchStreamRequest{AgentWorkID: "w1"}, message("w1", &sub, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), true},
		{"other Agent", WatchStreamRequest{AgentWorkID: "w1", Agent: &root}, message("w1", &sub, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), false},
		{"same Agent", WatchStreamRequest{AgentWorkID: "w1", Agent: &root}, message("w1", &root, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), true},
		{"kind outside", WatchStreamRequest{AgentWorkID: "w1", Kinds: []string{"UNIFIED_MESSAGE_KIND_RESULT"}}, message("w1", nil, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), false},
		{"turn outside", WatchStreamRequest{AgentWorkID: "w1", TurnIDs: []string{"t2"}}, message("w1", nil, "UNIFIED_MESSAGE_KIND_ASSISTANT", "t1"), false},
		{"unattributed error without opt-in", WatchStreamRequest{AgentWorkID: "w1", Agent: &root}, message("w1", nil, "UNIFIED_MESSAGE_KIND_ERROR", "t1"), false},
		{"unattributed error with opt-in", WatchStreamRequest{AgentWorkID: "w1", Agent: &root, IncludeUnattributedErrors: true}, message("w1", nil, "UNIFIED_MESSAGE_KIND_ERROR", "t1"), true},
		{"opt-in keeps the kind filter", WatchStreamRequest{AgentWorkID: "w1", Agent: &root, IncludeUnattributedErrors: true, Kinds: []string{"UNIFIED_MESSAGE_KIND_RESULT"}}, message("w1", nil, "UNIFIED_MESSAGE_KIND_ERROR", "t1"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newMessageScope(tc.request).includes(tc.message); got != tc.want {
				t.Fatalf("includes = %v, want %v", got, tc.want)
			}
		})
	}
}

// Readmission stays within a fifth of its interval either way, so streams
// opened together spread their questions to Agent.
func TestReadmissionIsJitteredWithinAFifth(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 200 {
		got := jittered(time.Minute)
		if got < 48*time.Second || got >= 72*time.Second {
			t.Fatalf("jittered = %s, want within 48s..72s", got)
		}
		seen[got] = true
	}
	if len(seen) < 2 {
		t.Fatal("readmission intervals do not vary")
	}
}

// A withdrawal ends the stream before anything still queued for the reader.
func TestAWithdrawalEndsTheStreamBeforeQueuedFrames(t *testing.T) {
	w := &WatchStream{options: WatchStreamOptions{}.withDefaults()}
	reader := newLiveReader("reader", &countedDrops{}, nil)
	reader.offer(message("m1"))
	reader.offer(&WorkAccessPayload{RevokedReaderID: "reader"})
	var sent []PortalLivePayload
	err := w.forward(t.Context(), reader, newMessageScope(WatchStreamRequest{AgentWorkID: "w1"}), func(p PortalLivePayload) error {
		sent = append(sent, p)
		return nil
	})
	if err != ErrWorkAccessWithdrawn {
		t.Fatalf("forward = %v, want ErrWorkAccessWithdrawn", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent %d frames after the withdrawal", len(sent))
	}
}

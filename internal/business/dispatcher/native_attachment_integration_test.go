//go:build integration

package dispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/websocket"

	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
)

// attachment is how the stand-in sandbox answers one attempt to attach.
type attachment int

const (
	// dropAttachment attaches, then loses the connection.
	dropAttachment attachment = iota
	// refuseAttachment cannot be reached at all.
	refuseAttachment
	// holdAttachment attaches and stays attached.
	holdAttachment
	// gateAttachment replays, says so on gated and attaches only once the
	// test closes release, then stays attached.
	gateAttachment
)

// flakySandbox is a sandbox whose execd answers each attempt to attach as
// planned, the last answer repeating.
type flakySandbox struct {
	mu      sync.Mutex
	plan    []attachment
	server  *httptest.Server
	gated   chan struct{}
	release chan struct{}
}

func newFlakySandbox(t *testing.T, plan ...attachment) *flakySandbox {
	t.Helper()
	s := &flakySandbox{plan: plan, gated: make(chan struct{}, 1), release: make(chan struct{})}
	attach := websocket.Handler(s.attach)
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.peek() == refuseAttachment {
			s.next()
			http.Error(w, "sandbox unreachable", http.StatusBadGateway)
			return
		}
		attach.ServeHTTP(w, r)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *flakySandbox) peek() attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.plan[0]
}

func (s *flakySandbox) next() attachment {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.plan[0]
	if len(s.plan) > 1 {
		s.plan = s.plan[1:]
	}
	return current
}

func (s *flakySandbox) attach(ws *websocket.Conn) {
	defer ws.Close()
	behaviour := s.next()
	replay := make([]byte, 9)
	replay[0] = 3
	if websocket.Message.Send(ws, replay) != nil {
		return
	}
	if behaviour == gateAttachment {
		s.gated <- struct{}{}
		select {
		case <-s.release:
		case <-ws.Request().Context().Done():
			return
		}
	}
	if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
		return
	}
	if behaviour == dropAttachment {
		// Long enough for the reader to take the attachment as held.
		time.Sleep(200 * time.Millisecond)
		return
	}
	<-ws.Request().Context().Done()
}

func (r *nativeRecords) attachmentReports() []command.ExecutionObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	var reports []command.ExecutionObservation
	for _, o := range r.observations {
		if o.Kind == command.ExecutionAttachmentLost || o.Kind == command.ExecutionAttachmentReturned {
			reports = append(reports, o)
		}
	}
	return reports
}

// reconnectAfter connects again once the n-th ended connection is reported,
// as Agent does on each report.
func reconnectAfter(t *testing.T, ctx context.Context, records *nativeRecords, n int, manager *dispatcher.NativeExecutions, target command.ConnectExecution) {
	t.Helper()
	require.Eventually(t, func() bool { return len(records.connectionsEnded()) >= n }, 15*time.Second, 20*time.Millisecond)
	require.NoError(t, manager.Connect(ctx, target))
}

func awaitAttached(t *testing.T, ctx context.Context, manager *dispatcher.NativeExecutions, target command.ConnectExecution) {
	t.Helper()
	require.Eventually(t, func() bool {
		observation, err := manager.ObserveProcess(ctx, target.WorkID, target.ProcessID)
		return err == nil && observation.Attached
	}, 15*time.Second, 20*time.Millisecond)
}

// A lost attachment that the next attempt regains reports nothing: the Work
// shows no Disconnection for a blip, and the observation says attached.
func TestABriefReattachmentReportsNoLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sandbox := newFlakySandbox(t, dropAttachment, holdAttachment)
	records := &nativeRecords{}
	manager := newNativeReplicas(t).replica("replica", records, newTestDelivery(t, records, records))
	target := command.ConnectExecution{WorkID: uuid.NewString(), ProcessID: "process", NativeSessionID: uuid.NewString(), Endpoint: sandbox.server.URL, AccessToken: "execd"}

	require.NoError(t, manager.Connect(ctx, target))
	reconnectAfter(t, ctx, records, 1, manager, target)
	awaitAttached(t, ctx, manager, target)
	require.Empty(t, records.attachmentReports())
}

// Once an attempt to attach again fails, Dispatcher reports the loss with
// the origin of the attachment's end, repeats it with the same origin on
// every failed attempt, on whichever replica, and reports the return once
// attached again.
func TestALostAttachmentIsReportedWithItsOriginUntilItReturns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sandbox := newFlakySandbox(t, dropAttachment, refuseAttachment, refuseAttachment, holdAttachment)
	records := &nativeRecords{}
	replicas := newNativeReplicas(t)
	first := replicas.replica("first", records, newTestDelivery(t, records, records))
	second := replicas.replica("second", records, newTestDelivery(t, records, records))
	target := command.ConnectExecution{WorkID: uuid.NewString(), ProcessID: "process", NativeSessionID: uuid.NewString(), Endpoint: sandbox.server.URL, AccessToken: "execd"}

	before := time.Now()
	require.NoError(t, first.Connect(ctx, target))
	reconnectAfter(t, ctx, records, 1, first, target)
	ended := time.Now()
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 1 }, 15*time.Second, 20*time.Millisecond)
	lost := records.attachmentReports()[0]
	require.Equal(t, command.ExecutionAttachmentLost, lost.Kind)
	require.Equal(t, target.ProcessID, lost.ProcessID)
	require.Equal(t, target.WorkID, lost.WorkID)
	require.False(t, lost.DetachedSince.Before(before.Truncate(time.Millisecond)) || lost.DetachedSince.After(ended), "the loss starts when the held attachment ended")

	// The next attempt runs on another replica, which resumes the loss from
	// the snapshot.
	reconnectAfter(t, ctx, records, 2, second, target)
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 2 }, 15*time.Second, 20*time.Millisecond)
	require.Equal(t, command.ExecutionAttachmentLost, records.attachmentReports()[1].Kind)
	require.True(t, lost.DetachedSince.Equal(records.attachmentReports()[1].DetachedSince), "a repeated loss keeps its origin")

	reconnectAfter(t, ctx, records, 3, second, target)
	awaitAttached(t, ctx, second, target)
	require.Eventually(t, func() bool { return len(records.attachmentReports()) == 3 }, 15*time.Second, 20*time.Millisecond)
	returned := records.attachmentReports()[2]
	require.Equal(t, command.ExecutionAttachmentReturned, returned.Kind)
	require.True(t, lost.DetachedSince.Equal(returned.DetachedSince), "the return names the loss it ends")
	require.Equal(t, target.ProcessID, returned.ProcessID)
}

package messagedelivery

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
)

type deliveryRecorder struct {
	mu          sync.Mutex
	appends     [][]string
	live        []string
	diagnostics []Diagnostic
	appendErr   error
	block       chan struct{}
}

func (r *deliveryRecorder) Append(_ context.Context, messages ...*dispatcherpublicv1.UnifiedMessage) error {
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for _, m := range messages {
		ids = append(ids, m.GetId())
	}
	r.appends = append(r.appends, ids)
	return r.appendErr
}

func (r *deliveryRecorder) PublishLive(_ context.Context, msg *dispatcherpublicv1.UnifiedMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.live = append(r.live, msg.GetId())
	return nil
}

func (r *deliveryRecorder) PublishDiagnostic(_ context.Context, d Diagnostic) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.diagnostics = append(r.diagnostics, d)
	return nil
}

func (r *deliveryRecorder) snapshot() ([][]string, []string, []Diagnostic) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.appends...), append([]string(nil), r.live...), append([]Diagnostic(nil), r.diagnostics...)
}

func newRecordingSink(t *testing.T, r *deliveryRecorder) *Sink {
	t.Helper()
	sink, err := NewSink(r, r, r, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close(context.Background()) })
	return sink
}

func message(id string, kind dispatcherpublicv1.UnifiedMessageKind) *dispatcherpublicv1.UnifiedMessage {
	return &dispatcherpublicv1.UnifiedMessage{Id: id, AgentWorkId: "work", Kind: kind}
}

// Intent (real): messages are batched for history and published live without
// the producer waiting; a RESULT is appended, with everything before it, before
// DeliverResult returns.
func TestSinkBatchesAndAppendsAResultBeforeReturning(t *testing.T) {
	r := &deliveryRecorder{}
	sink := newRecordingSink(t, r)
	sink.Deliver(message("a", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION))
	sink.Deliver(message("b", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION))
	require.NoError(t, sink.DeliverResult(context.Background(), message("r", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT)))
	appends, _, _ := r.snapshot()
	require.Equal(t, [][]string{{"a", "b", "r"}}, appends, "one batch, the RESULT included")
	require.Eventually(t, func() bool { _, live, _ := r.snapshot(); return len(live) == 3 }, time.Second, 10*time.Millisecond)

	sink.Deliver(message("c", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION))
	require.Eventually(t, func() bool { appends, _, _ := r.snapshot(); return len(appends) == 2 }, time.Second, 10*time.Millisecond, "a batch is appended after its interval")
}

// Intent (real): a RESULT the log could not keep is reported to the caller and
// told live; the producer of ordinary messages never waits on the log.
func TestSinkReportsAResultAppendFailure(t *testing.T) {
	r := &deliveryRecorder{appendErr: errors.New("unavailable")}
	sink := newRecordingSink(t, r)
	require.ErrorIs(t, sink.DeliverResult(context.Background(), message("r", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT)), r.appendErr)
	_, _, diagnostics := r.snapshot()
	require.Len(t, diagnostics, 1)
	require.Equal(t, failureStorageUnavailable, diagnostics[0].Reason)

	blocked := &deliveryRecorder{block: make(chan struct{})}
	slow := newRecordingSink(t, blocked)
	slow.Deliver(message("first", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION))
	done := make(chan struct{})
	go func() {
		for range 100 {
			slow.Deliver(message("next", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Deliver waited on a slow message log")
	}
	close(blocked.block)
}

// Intent (real): a notice reaches live readers only; it is never stored.
func TestSinkNoticeIsLiveOnly(t *testing.T) {
	r := &deliveryRecorder{}
	require.NoError(t, newRecordingSink(t, r).PublishNotice(context.Background(), "work-1", "model_rate_limited", "API Error: 429"))
	appends, live, diagnostics := r.snapshot()
	require.Empty(t, appends)
	require.Empty(t, live)
	require.Len(t, diagnostics, 1)
	require.Equal(t, "warning", diagnostics[0].Severity)
}

// Intent (real): closing appends what was handed over.
func TestSinkCloseAppendsQueuedMessages(t *testing.T) {
	r := &deliveryRecorder{}
	sink, err := NewSink(r, r, r, slog.Default())
	require.NoError(t, err)
	sink.Deliver(message("a", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION))
	require.NoError(t, sink.Close(context.Background()))
	appends, _, _ := r.snapshot()
	require.Equal(t, [][]string{{"a"}}, appends)
	require.ErrorIs(t, sink.DeliverResult(context.Background(), message("r", dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT)), ErrSinkClosed)
}

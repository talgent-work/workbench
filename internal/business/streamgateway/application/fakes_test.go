package application_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/streamgateway/public/v1/streamgatewaypublicv1connect"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	streamrpc "github.com/talgent-work/workbench/internal/business/streamgateway/transport/connectrpc"
)

func unifiedMessage(id, sessionID string, ts time.Time) *query.Message {
	return &query.Message{
		ID:          id,
		AgentWorkID: sessionID,
		Kind:        "UNIFIED_MESSAGE_KIND_ASSISTANT",
		Timestamp:   ts,
		Text:        &query.TextMessage{},
	}
}

type fakeStore struct {
	toolDetailFn func(context.Context, query.ToolDetailRequest) (query.ToolDetail, error)
	detailFn     func(context.Context, query.MessageDetailRequest) (query.MessageDetail, error)
	mu           sync.Mutex
	rows         []*query.Message
	calls        []query.ReplayCursor
	listFn       func(context.Context, string, query.ReplayCursor) ([]*query.Message, error)

	messageRows  []*query.Message
	messageCalls []query.ListMessagesBySessionRequest
	messageFn    func(context.Context, query.ListMessagesBySessionRequest) ([]*query.Message, error)
}

func (f *fakeStore) GetMessageDetail(ctx context.Context, request query.MessageDetailRequest) (query.MessageDetail, error) {
	if f.detailFn != nil {
		return f.detailFn(ctx, request)
	}
	return query.MessageDetail{}, query.ErrMessageNotFound
}

type allowWorkRead struct{}

func (allowWorkRead) AuthorizeRead(context.Context, string, query.ReaderAdmission, string) error {
	return nil
}

func (allowWorkRead) AdmitStreamReader(context.Context, string, query.ReaderAdmission, string) (*query.WorkState, error) {
	return nil, nil
}

func (f *fakeStore) ListReplay(ctx context.Context, sessionID string, opts query.ReplayCursor) ([]*query.Message, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	fn := f.listFn
	rows := append([]*query.Message(nil), f.rows...)
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, sessionID, opts)
	}
	return rows, nil
}

func (f *fakeStore) ListMessagesBySession(ctx context.Context, request query.ListMessagesBySessionRequest) ([]*query.Message, error) {
	f.mu.Lock()
	f.messageCalls = append(f.messageCalls, request)
	fn := f.messageFn
	rows := append([]*query.Message(nil), f.messageRows...)
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, request)
	}
	return rows, nil
}

func (f *fakeStore) recordedCalls() []query.ReplayCursor {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]query.ReplayCursor(nil), f.calls...)
}

func (f *fakeStore) recordedMessageCalls() []query.ListMessagesBySessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]query.ListMessagesBySessionRequest(nil), f.messageCalls...)
}

type fakeSubscription struct {
	ch      chan query.PortalLivePayload
	closed  chan struct{}
	onClose func()
}

func newFakeSubscription(buf int) *fakeSubscription {
	return &fakeSubscription{ch: make(chan query.PortalLivePayload, buf), closed: make(chan struct{})}
}

func (f *fakeSubscription) Recv(ctx context.Context) (query.PortalLivePayload, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.closed:
		return nil, context.Canceled
	case msg := <-f.ch:
		return msg, nil
	}
}

func (f *fakeSubscription) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
		if f.onClose != nil {
			f.onClose()
		}
	}
	return nil
}

type fakeStream struct {
	mu     sync.Mutex
	subs   map[string]*fakeSubscription
	subErr error
}

func newFakeStream() *fakeStream {
	return &fakeStream{subs: make(map[string]*fakeSubscription)}
}

func (f *fakeStream) SubscribeSession(_ context.Context, sessionID string) (query.PortalLiveSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subErr != nil {
		return nil, f.subErr
	}
	sub := newFakeSubscription(16)
	// Only forget ourselves — a subsequent SubscribeSession may have installed
	// a different sub under the same sessionID while our owning hub was still
	// tearing down, and we must not clobber it.
	sub.onClose = func() { f.forgetSubIfMatch(sessionID, sub) }
	f.subs[sessionID] = sub
	return sub, nil
}

// publish lets tests push a live message into the work's active subscription.
func (f *fakeStream) publish(sessionID string, msg *query.Message) {
	f.mu.Lock()
	sub := f.subs[sessionID]
	f.mu.Unlock()
	if sub != nil {
		sub.ch <- &query.UnifiedMessagePayload{Message: msg}
	}
}

func (f *fakeStream) publishWorkState(sessionID string, frame *query.WorkState) {
	f.mu.Lock()
	sub := f.subs[sessionID]
	f.mu.Unlock()
	if sub != nil {
		sub.ch <- &query.WorkStatePayload{Frame: frame}
	}
}

func (f *fakeStream) publishDiagnostic(sessionID string, frame *query.Diagnostic) {
	f.mu.Lock()
	sub := f.subs[sessionID]
	f.mu.Unlock()
	if sub != nil {
		sub.ch <- &query.DiagnosticPayload{Frame: frame}
	}
}

// closeSession simulates the upstream broker subscription ending for a work.
func (f *fakeStream) closeSession(sessionID string) {
	f.mu.Lock()
	sub := f.subs[sessionID]
	f.mu.Unlock()
	if sub != nil {
		_ = sub.Close()
	}
}

func (f *fakeStream) activeSubs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// forgetSubIfMatch removes the subscription entry only if it still points
// to the given sub. Prevents a closing old sub from clobbering a freshly
// installed replacement during Acquire-during-eviction races.
func (f *fakeStream) forgetSubIfMatch(sessionID string, sub *fakeSubscription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if cur, ok := f.subs[sessionID]; ok && cur == sub {
		delete(f.subs, sessionID)
	}
}

func slogDiscard(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func slogBuffer(t *testing.T) (*slog.Logger, *lockedBuffer) {
	t.Helper()
	var buf lockedBuffer
	return slog.New(slog.NewJSONHandler(&buf, nil)), &buf
}

func recvWithTimeout(t *testing.T, sub *query.Subscription) query.PortalLivePayload {
	t.Helper()
	select {
	case msg := <-sub.Live():
		return msg
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Live() recv timed out")
		return nil
	}
}

func (f *fakeStore) GetToolDetail(ctx context.Context, request query.ToolDetailRequest) (query.ToolDetail, error) {
	if f.toolDetailFn != nil {
		return f.toolDetailFn(ctx, request)
	}
	return query.ToolDetail{}, nil
}

// watched reads a WatchStream frame by frame: Receive reports whether a frame
// arrived, Msg is that frame, and Err is what ended the stream, nil for its
// clean end.
type watched struct {
	stream streamgatewaypublicv1connect.StreamGatewayServiceWatchStreamClientStream
	msg    *streamgatewaypublicv1.WatchStreamResponse
	err    error
}

func watch(stream streamgatewaypublicv1connect.StreamGatewayServiceWatchStreamClientStream, err error) (*watched, error) {
	if err != nil {
		return nil, err
	}
	return &watched{stream: stream}, nil
}

func (w *watched) Receive() bool {
	msg, err := w.stream.Receive()
	if err != nil {
		if !errors.Is(err, io.EOF) {
			w.err = err
		}
		return false
	}
	w.msg = msg
	return true
}

func (w *watched) Msg() *streamgatewaypublicv1.WatchStreamResponse { return w.msg }

func (w *watched) Err() error { return w.err }

func (w *watched) Close() error { return w.stream.Close() }

// served is a client of the stream gateway's Connect handler for app, in
// process, as a served call reaches it.
func served(app *application.Application) streamgatewaypublicv1connect.StreamGatewayServiceClient {
	server := connect.NewServer()
	streamgatewaypublicv1connect.RegisterStreamGatewayServiceHandler(server, streamrpc.NewHandler(app))
	return streamgatewaypublicv1connect.NewStreamGatewayServiceClient(connect.NewClient(connectinprocess.New(server)))
}

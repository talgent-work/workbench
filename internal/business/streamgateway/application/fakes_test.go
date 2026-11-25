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
	streamgatewaypublicv1 "github.com/talgent-work/contracts/gen/go/streamgateway/public/v1"
	"github.com/talgent-work/contracts/gen/go/streamgateway/public/v1/streamgatewaypublicv1connect"
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

// allowWorkRead admits every reader; an admitted stream starts from the
// Work's running state, so a client knows its stream is listening.
type allowWorkRead struct{}

func (allowWorkRead) AuthorizeRead(context.Context, string, query.ReaderAdmission, string) error {
	return nil
}

func (allowWorkRead) AdmitStreamReader(_ context.Context, _ string, _ query.ReaderAdmission, work string) (*query.WorkState, error) {
	return &query.WorkState{AgentWorkID: work, State: "running"}, nil
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

func (f *fakeStore) recordedMessageCalls() []query.ListMessagesBySessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]query.ListMessagesBySessionRequest(nil), f.messageCalls...)
}

// fakeSubscription is one Work's live subscription in a fakeStream.
type fakeSubscription struct {
	deliver   func(query.PortalLivePayload)
	ended     chan struct{}
	endOnce   sync.Once
	closeOnce sync.Once
	onClose   func()
}

func (f *fakeSubscription) Ended() <-chan struct{} { return f.ended }

func (f *fakeSubscription) Close() error {
	f.closeOnce.Do(f.onClose)
	return nil
}

// fakeStream hands published frames straight to the Work's subscription.
type fakeStream struct {
	mu         sync.Mutex
	subs       map[string]*fakeSubscription
	subscribes int
	subErr     error
}

func newFakeStream() *fakeStream {
	return &fakeStream{subs: make(map[string]*fakeSubscription)}
}

func (f *fakeStream) Subscribe(_ context.Context, workID string, deliver func(query.PortalLivePayload)) (query.PortalLiveSubscription, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subErr != nil {
		return nil, f.subErr
	}
	f.subscribes++
	sub := &fakeSubscription{deliver: deliver, ended: make(chan struct{})}
	sub.onClose = func() { f.forget(workID, sub) }
	f.subs[workID] = sub
	return sub, nil
}

func (f *fakeStream) send(workID string, payload query.PortalLivePayload) {
	f.mu.Lock()
	sub := f.subs[workID]
	f.mu.Unlock()
	if sub != nil {
		sub.deliver(payload)
	}
}

func (f *fakeStream) publish(workID string, msg *query.Message) {
	f.send(workID, &query.UnifiedMessagePayload{Message: msg})
}

func (f *fakeStream) publishWorkState(workID string, frame *query.WorkState) {
	f.send(workID, &query.WorkStatePayload{Frame: frame})
}

func (f *fakeStream) publishDiagnostic(workID string, frame *query.Diagnostic) {
	f.send(workID, &query.DiagnosticPayload{Frame: frame})
}

func (f *fakeStream) publishAccess(workID string, payload *query.WorkAccessPayload) {
	f.send(workID, payload)
}

// endSession ends the Work's subscription as a connection closed for good does.
func (f *fakeStream) endSession(workID string) {
	f.mu.Lock()
	sub := f.subs[workID]
	f.mu.Unlock()
	if sub != nil {
		sub.endOnce.Do(func() { close(sub.ended) })
	}
}

func (f *fakeStream) activeSubs() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

func (f *fakeStream) forget(workID string, sub *fakeSubscription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subs[workID] == sub {
		delete(f.subs, workID)
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

// discardDrops counts nothing.
type discardDrops struct{}

func (discardDrops) Dropped(string) {}

// newTestApplication serves stream and reader with every reader admitted.
func newTestApplication(reader query.UnifiedMessageReader, stream query.PortalLiveStream, logger *slog.Logger, options ...query.WatchStreamOptions) *application.Application {
	return newAccessApplication(reader, stream, logger, allowWorkRead{}, options...)
}

func newAccessApplication(reader query.UnifiedMessageReader, stream query.PortalLiveStream, logger *slog.Logger, access query.StreamAccessPort, options ...query.WatchStreamOptions) *application.Application {
	var opts query.WatchStreamOptions
	if len(options) > 0 {
		opts = options[0]
	}
	return application.NewApplication(query.NewWatchStream(stream, discardDrops{}, logger, access, opts), query.NewListMessagesBySessionHandler(reader, access), reader, access)
}

// receiveStart reads the Work state an admitted stream starts with.
func receiveStart(t *testing.T, stream *watched) {
	t.Helper()
	if !stream.Receive() {
		t.Fatalf("starting state: %v", stream.Err())
	}
	if stream.Msg().GetWorkState() == nil {
		t.Fatalf("first frame = %v, want the starting Work state", stream.Msg())
	}
}

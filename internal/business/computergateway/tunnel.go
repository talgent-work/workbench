package computergateway

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

// streamBacklog bounds the frames a stream holds that its reader has not
// taken; a reader further behind loses the stream and attaches again from its
// offset. The stream's end always finds
// room beyond them, so a reader tells a stream that ended from one it lost.
const streamBacklog = 256

// fileWindow bounds the bytes of a file read the machine sends beyond those
// the Gateway credited back, and so the bytes the Gateway holds for the read's
// reader; credit goes back in steps of creditStep as the reader takes bytes.
const (
	fileWindow = 1 << 20
	creditStep = fileWindow / 4
)

// errTunnelClosed answers a relayed call whose machine connection ended.
var errTunnelClosed = errors.New("computer gateway: the machine's connection ended")

// relayFrame is a tunnel text frame; which fields are set depends on Type.
type relayFrame struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	StreamID  string          `json:"stream_id,omitempty"`
	WorkID    string          `json:"work_id,omitempty"`
	ProcessID string          `json:"process_id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Path      string          `json:"path,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Token     string          `json:"token,omitempty"`
	Since     int64           `json:"since,omitempty"`
	Status    int             `json:"status,omitempty"`
	Text      string          `json:"text,omitempty"`
	Bytes     int             `json:"bytes,omitempty"`
	Body      json.RawMessage `json:"body,omitempty"`
}

// streamFrame is one pipe or file read frame from the machine, or its end.
type streamFrame struct {
	kind   websocket.MessageType
	data   []byte
	closed bool
	status int
}

// stream is one open pipe or file read and what its reader has not taken.
type stream struct {
	frames chan streamFrame
	// window bounds held, the bytes of a file read buffered or being written
	// for its reader; 0 for a pipe, bounded by frames alone.
	window, held int
	// taken counts bytes the reader took that are not yet credited back.
	taken int
}

// tunnel multiplexes relayed execd calls over one machine connection.
type tunnel struct {
	conn  *websocket.Conn
	write sync.Mutex

	mu       sync.Mutex
	closed   bool
	requests map[string]chan relayFrame
	streams  map[uuid.UUID]*stream
}

func newTunnel(conn *websocket.Conn) *tunnel {
	return &tunnel{conn: conn, requests: map[string]chan relayFrame{}, streams: map[uuid.UUID]*stream{}}
}

func (t *tunnel) send(ctx context.Context, kind websocket.MessageType, data []byte) error {
	t.write.Lock()
	defer t.write.Unlock()
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return t.conn.Write(ctx, kind, data)
}

// sendJSON writes a text frame: a relay frame or a connection protocol
// message.
func (t *tunnel) sendJSON(ctx context.Context, frame any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return t.send(ctx, websocket.MessageText, data)
}

// call relays one request and waits for its response.
func (t *tunnel) call(ctx context.Context, request relayFrame) (relayFrame, error) {
	request.Type, request.RequestID = "exec_request", uuid.NewString()
	answer := make(chan relayFrame, 1)
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return relayFrame{}, errTunnelClosed
	}
	t.requests[request.RequestID] = answer
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.requests, request.RequestID)
		t.mu.Unlock()
	}()
	if err := t.sendJSON(ctx, request); err != nil {
		return relayFrame{}, err
	}
	select {
	case response, ok := <-answer:
		if !ok {
			return relayFrame{}, errTunnelClosed
		}
		return response, nil
	case <-ctx.Done():
		return relayFrame{}, ctx.Err()
	}
}

// open starts a pipe or file read stream; frames arrive on the returned
// channel until it is closed, the stream's end last when it ended rather than
// was lost.
func (t *tunnel) open(ctx context.Context, request relayFrame) (uuid.UUID, <-chan streamFrame, error) {
	id := uuid.New()
	opened := &stream{frames: make(chan streamFrame, streamBacklog+1)}
	if request.Kind == "file" {
		opened.window = fileWindow
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return id, nil, errTunnelClosed
	}
	t.streams[id] = opened
	t.mu.Unlock()
	request.Type, request.StreamID = "stream_open", id.String()
	if err := t.sendJSON(ctx, request); err != nil {
		t.forget(id)
		return id, nil, err
	}
	return id, opened.frames, nil
}

// credit returns bytes a file read's reader took to the machine's window,
// telling the machine once they make a step.
func (t *tunnel) credit(ctx context.Context, id uuid.UUID, bytes int) error {
	t.mu.Lock()
	s, ok := t.streams[id]
	if !ok {
		t.mu.Unlock()
		return nil
	}
	s.held -= bytes
	s.taken += bytes
	grant := 0
	if s.taken >= creditStep {
		grant, s.taken = s.taken, 0
	}
	t.mu.Unlock()
	if grant == 0 {
		return nil
	}
	return t.sendJSON(ctx, relayFrame{Type: "stream_credit", StreamID: id.String(), Bytes: grant})
}

// endStream tells the machine the stream ended and forgets it.
func (t *tunnel) endStream(ctx context.Context, id uuid.UUID) {
	if t.forget(id) {
		_ = t.sendJSON(ctx, relayFrame{Type: "stream_close", StreamID: id.String()})
	}
}

// forget drops a stream and reports whether it was still open.
func (t *tunnel) forget(id uuid.UUID) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, ok := t.streams[id]
	if ok {
		delete(t.streams, id)
		close(s.frames)
	}
	return ok
}

// writeStream sends a client pipe frame to the machine.
func (t *tunnel) writeStream(ctx context.Context, id uuid.UUID, kind websocket.MessageType, data []byte) error {
	if kind == websocket.MessageText {
		return t.sendJSON(ctx, relayFrame{Type: "stream_text", StreamID: id.String(), Text: string(data)})
	}
	return t.send(ctx, websocket.MessageBinary, append(id[:], data...))
}

// deliver routes a frame from the machine; it reports whether the frame was
// relay traffic.
func (t *tunnel) deliver(ctx context.Context, kind websocket.MessageType, data []byte) bool {
	if kind == websocket.MessageBinary {
		if len(data) < len(uuid.UUID{})+1 {
			return true
		}
		id, _ := uuid.FromBytes(data[:16])
		t.push(ctx, id, streamFrame{kind: websocket.MessageBinary, data: data[16:]})
		return true
	}
	var frame relayFrame
	if json.Unmarshal(data, &frame) != nil {
		return false
	}
	switch frame.Type {
	case "exec_response":
		t.mu.Lock()
		if answer, ok := t.requests[frame.RequestID]; ok {
			delete(t.requests, frame.RequestID)
			answer <- frame // buffered for exactly this one answer
		}
		t.mu.Unlock()
	case "stream_text":
		if id, err := uuid.Parse(frame.StreamID); err == nil {
			t.push(ctx, id, streamFrame{kind: websocket.MessageText, data: []byte(frame.Text)})
		}
	case "stream_close":
		if id, err := uuid.Parse(frame.StreamID); err == nil {
			t.mu.Lock()
			s, ok := t.streams[id]
			if ok {
				delete(t.streams, id)
				s.frames <- streamFrame{closed: true, status: frame.Status} // room is kept for the end
				close(s.frames)
			}
			t.mu.Unlock()
		}
	default:
		return false
	}
	return true
}

// push hands a frame to its stream's reader; a reader that fell too far
// behind, or a file read past its window, loses the stream.
func (t *tunnel) push(ctx context.Context, id uuid.UUID, frame streamFrame) {
	t.mu.Lock()
	s, ok := t.streams[id]
	if !ok {
		t.mu.Unlock()
		return
	}
	if len(s.frames) < streamBacklog && (s.window == 0 || s.held+len(frame.data) <= s.window) {
		if s.window > 0 {
			s.held += len(frame.data)
		}
		s.frames <- frame
		t.mu.Unlock()
	} else {
		delete(t.streams, id)
		close(s.frames)
		t.mu.Unlock()
		// Telling the machine waits for the connection's writer; the read
		// loop that called push must not, or one slow reader would stall
		// heartbeats and every other stream.
		go func() {
			_ = t.sendJSON(context.WithoutCancel(ctx), relayFrame{Type: "stream_close", StreamID: id.String()})
		}()
	}
}

// close ends every waiting call and stream with the connection.
func (t *tunnel) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for id, answer := range t.requests {
		close(answer)
		delete(t.requests, id)
	}
	for id, s := range t.streams {
		close(s.frames)
		delete(t.streams, id)
	}
}

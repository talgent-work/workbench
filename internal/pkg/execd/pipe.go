package execd

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/samber/oops"
	"golang.org/x/net/websocket"
)

// Frame preserves stream identity. Replay contains mixed historical output in
// execd's protocol and must never be interpreted as live native stdout.
type Frame struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Role      string `json:"role,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	Code      string `json:"code,omitempty"`
	Offset    int64  `json:"offset,omitempty"`
	Data      []byte `json:"-"`
}

type Pipe struct {
	conn  *websocket.Conn
	id    string
	write sync.Mutex
}

// Receive has one caller. Cancellation closes the socket to unblock I/O; it
// does not establish process exit or stop the remote process.
func (p *Pipe) Receive(ctx context.Context) (Frame, error) {
	if err := ctx.Err(); err != nil {
		return Frame{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = p.conn.Close() })
	defer stop()
	var frame Frame
	err := frames.Receive(p.conn, &frame)
	if ctx.Err() != nil {
		return Frame{}, ctx.Err()
	}
	if err != nil {
		return Frame{}, oops.With("operation", "receive").Wrap(err)
	}
	if frame.Type == "connected" && (frame.SessionID != p.id || frame.Role != "holder") {
		return Frame{}, errors.New("execd: pipe ownership mismatch")
	}
	return frame, nil
}

func (p *Pipe) WriteInput(ctx context.Context, data []byte) error {
	frame := make([]byte, 1+len(data))
	copy(frame[1:], data)
	return p.send(ctx, frame)
}

func (p *Pipe) Signal(ctx context.Context, signal string) error {
	switch signal {
	case "SIGINT", "SIGTERM", "SIGKILL":
	default:
		return errors.New("execd: unsupported process signal")
	}
	frame, _ := json.Marshal(struct {
		Type   string `json:"type"`
		Signal string `json:"signal"`
	}{"signal", signal})
	return p.send(ctx, string(frame))
}

func (p *Pipe) send(ctx context.Context, frame any) error {
	p.write.Lock()
	defer p.write.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := p.conn.SetWriteDeadline(deadline); err != nil {
		return oops.With("operation", "write deadline").Wrap(err)
	}
	stop := context.AfterFunc(ctx, func() { _ = p.conn.Close() })
	defer stop()
	err := websocket.Message.Send(p.conn, frame)
	// Receive also writes WebSocket pong frames. A completed input's deadline
	// must not make a later heartbeat fail and detach the live process stream.
	resetErr := p.conn.SetWriteDeadline(time.Time{})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return oops.With("operation", "send").Wrap(err)
	}
	if resetErr != nil {
		return oops.With("operation", "reset write deadline").Wrap(resetErr)
	}
	return nil
}

// Close detaches the stream, preserving the process for its execution owner.
func (p *Pipe) Close() error { return p.conn.Close() }

var frames = websocket.Codec{Unmarshal: func(data []byte, payloadType byte, target any) error {
	f := target.(*Frame)
	switch payloadType {
	case websocket.TextFrame:
		if err := json.Unmarshal(data, f); err != nil {
			return err
		}
		switch f.Type {
		case "connected":
			if f.SessionID == "" || f.Mode != "pipe" {
				return errors.New("execd: invalid pipe connection")
			}
		case "exit":
			if f.ExitCode == nil {
				return errors.New("execd: exit frame missing exit code")
			}
		case "error":
			return fmt.Errorf("execd: stream error %s", f.Code)
		default:
			return errors.New("execd: unsupported control frame")
		}
	case websocket.BinaryFrame:
		if len(data) == 0 {
			return errors.New("execd: empty binary frame")
		}
		switch data[0] {
		case 1:
			f.Type = "stdout"
		case 2:
			f.Type = "stderr"
		case 3:
			if len(data) < 9 {
				return errors.New("execd: invalid replay frame")
			}
			f.Type = "replay"
			f.Offset = int64(binary.BigEndian.Uint64(data[1:9]))
			data = data[8:]
		default:
			return errors.New("execd: unsupported binary frame")
		}
		f.Data = data[1:]
	default:
		return errors.New("execd: unsupported WebSocket frame")
	}
	return nil
}}

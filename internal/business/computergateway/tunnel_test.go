package computergateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// tunnelPair is a tunnel over a live connection and the machine's end of it.
// The tunnel's read loop routes frames as the connection handler does and
// closes the tunnel when the connection ends.
func tunnelPair(t *testing.T) (*tunnel, *websocket.Conn) {
	t.Helper()
	held := make(chan *tunnel, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(machineReadLimit)
		relay := newTunnel(conn)
		held <- relay
		defer relay.close()
		for {
			kind, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			relay.deliver(context.Background(), kind, data)
		}
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	machine, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = machine.CloseNow() })
	return <-held, machine
}

// machineReads reads the machine's next text frame.
func machineReads(t *testing.T, machine *websocket.Conn) relayFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := machine.Read(ctx)
	require.NoError(t, err)
	var frame relayFrame
	require.NoError(t, json.Unmarshal(data, &frame))
	return frame
}

func machineStreams(t *testing.T, machine *websocket.Conn, id uuid.UUID, frames int) {
	t.Helper()
	for range frames {
		require.NoError(t, machine.Write(context.Background(), websocket.MessageBinary, append(id[:], 0x01, 'x')))
	}
}

func TestACallEndsWithItsConnection(t *testing.T) {
	relay, machine := tunnelPair(t)
	answered := make(chan error, 1)
	go func() {
		_, err := relay.call(context.Background(), relayFrame{WorkID: "w", Method: http.MethodGet, Path: "/ping"})
		answered <- err
	}()
	require.Equal(t, "exec_request", machineReads(t, machine).Type)
	require.NoError(t, machine.Close(websocket.StatusNormalClosure, ""))
	select {
	case err := <-answered:
		require.ErrorIs(t, err, errTunnelClosed)
	case <-time.After(5 * time.Second):
		t.Fatal("the call outlived its connection")
	}
	_, err := relay.call(context.Background(), relayFrame{WorkID: "w", Method: http.MethodGet, Path: "/ping"})
	require.ErrorIs(t, err, errTunnelClosed, "a closed tunnel takes no new call")
}

func TestAReaderTooFarBehindLosesItsStream(t *testing.T) {
	relay, machine := tunnelPair(t)
	id, frames, err := relay.open(context.Background(), relayFrame{WorkID: "w", ProcessID: "p"})
	require.NoError(t, err)
	require.Equal(t, "stream_open", machineReads(t, machine).Type)
	machineStreams(t, machine, id, streamBacklog+1)
	closing := machineReads(t, machine)
	require.Equal(t, relayFrame{Type: "stream_close", StreamID: id.String()}, closing, "the machine is told the stream ended")
	taken := 0
	for frame := range frames {
		require.False(t, frame.closed)
		taken++
	}
	require.Equal(t, streamBacklog, taken, "the reader keeps what was held, then the stream ends")
}

func TestAStreamClosedWhileItsBacklogIsFullStillEnds(t *testing.T) {
	relay, machine := tunnelPair(t)
	id, frames, err := relay.open(context.Background(), relayFrame{WorkID: "w", ProcessID: "p"})
	require.NoError(t, err)
	machineReads(t, machine)
	machineStreams(t, machine, id, streamBacklog)
	require.Eventually(t, func() bool { return len(frames) == streamBacklog }, 5*time.Second, time.Millisecond)
	data, err := json.Marshal(relayFrame{Type: "stream_close", StreamID: id.String()})
	require.NoError(t, err)
	require.NoError(t, machine.Write(context.Background(), websocket.MessageText, data))
	require.Eventually(t, func() bool {
		relay.mu.Lock()
		defer relay.mu.Unlock()
		_, open := relay.streams[id]
		return !open
	}, 5*time.Second, time.Millisecond, "the stream ends before its reader takes anything")
	taken, ended := 0, false
	for frame := range frames {
		if frame.closed {
			ended = true
			continue
		}
		taken++
	}
	require.Equal(t, streamBacklog, taken)
	require.True(t, ended, "the reader learns the stream ended rather than was lost")
}

// A file read holds at most its window for the reader: credit returns the
// bytes the reader took to the machine in steps, and a machine sending past
// its window loses the stream.
func TestAFileReadHoldsAtMostItsWindow(t *testing.T) {
	relay, machine := tunnelPair(t)
	id, frames, err := relay.open(context.Background(), relayFrame{WorkID: "w", Kind: "file", Path: "a"})
	require.NoError(t, err)
	require.Equal(t, "stream_open", machineReads(t, machine).Type)
	piece := append(id[:], make([]byte, machineChunk)...)
	for range fileWindow / machineChunk {
		require.NoError(t, machine.Write(context.Background(), websocket.MessageBinary, piece))
	}
	for range creditStep / machineChunk {
		frame := <-frames
		require.NoError(t, relay.credit(context.Background(), id, len(frame.data)))
	}
	require.Equal(t, relayFrame{Type: "stream_credit", StreamID: id.String(), Bytes: creditStep}, machineReads(t, machine), "the bytes taken are credited back once they make a step")

	for range creditStep / machineChunk {
		require.NoError(t, machine.Write(context.Background(), websocket.MessageBinary, piece))
	}
	require.NoError(t, machine.Write(context.Background(), websocket.MessageBinary, append(id[:], 'x')))
	require.Equal(t, relayFrame{Type: "stream_close", StreamID: id.String()}, machineReads(t, machine), "a machine past its window loses the stream")
	held := 0
	for frame := range frames {
		require.False(t, frame.closed)
		held += len(frame.data)
	}
	require.Equal(t, fileWindow, held, "the Gateway held no more than the window, and the reader keeps it before the stream ends")
}

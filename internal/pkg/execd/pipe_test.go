package execd_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"golang.org/x/net/websocket"
)

// A successful input write must not poison a later ping reply on the read path.
func TestPipeHeartbeatAfterSuccessfulInputDeadline(t *testing.T) {
	for _, writeInput := range []bool{false, true} {
		name := "without_prior_write"
		if writeInput {
			name = "after_successful_write"
		}
		t.Run(name, func(t *testing.T) {
			pingNow := make(chan struct{})
			release := make(chan struct{})
			serverErr := make(chan error, 1)
			server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				if writeInput {
					var b []byte
					if err := websocket.Message.Receive(ws, &b); err != nil {
						serverErr <- err
						return
					}
				}
				select {
				case <-pingNow:
				case <-release:
					return
				}
				ping := websocket.Codec{Marshal: func(any) ([]byte, byte, error) { return []byte("heartbeat"), websocket.PingFrame, nil }}
				if err := ping.Send(ws, nil); err != nil {
					serverErr <- err
					return
				}
				if err := websocket.Message.Send(ws, []byte{1, 'o', 'k'}); err != nil {
					serverErr <- err
					return
				}
				serverErr <- nil
				<-release
			}))
			defer server.Close()
			defer close(release)
			client, err := execd.NewClient(server.Client(), server.URL, "test-only")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			pipe, err := client.Attach(ctx, "process", 0)
			require.NoError(t, err)
			defer pipe.Close()
			writeCtx, done := context.WithTimeout(ctx, 200*time.Millisecond)
			defer done()
			if writeInput {
				require.NoError(t, pipe.WriteInput(writeCtx, []byte("input\n")))
			}
			<-writeCtx.Done()
			close(pingNow)
			frame, err := pipe.Receive(ctx)
			select {
			case serverResult := <-serverErr:
				require.NoError(t, serverResult, "server sent ping and stdout")
			case <-ctx.Done():
				t.Fatal("server did not finish sending heartbeat and stdout")
			}
			require.NoError(t, err, "successful input deadline must not break later pong from Receive")
			require.Equal(t, "stdout", frame.Type)
			require.Equal(t, "ok", string(frame.Data))
		})
	}
}

package execd_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"golang.org/x/net/websocket"
)

func TestTerminateWaitsForExitAndRecoversAfterObservationCancellation(t *testing.T) {
	var exited, deleted atomic.Bool
	var signals atomic.Int32
	signaled := make(chan struct{}, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /pty/process", func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-only", r.Header.Get("X-EXECD-ACCESS-TOKEN"))
		if deleted.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"session_id":"process","running":true}`))
	})
	mux.Handle("GET /pty/process/ws", websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		var signal string
		if websocket.Message.Receive(ws, &signal) != nil {
			return
		}
		require.JSONEq(t, `{"type":"signal","signal":"SIGTERM"}`, signal)
		signals.Add(1)
		signaled <- struct{}{}
		if !exited.Load() {
			_ = websocket.Message.Receive(ws, &signal) // observation deadline detaches
			return
		}
		_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":143}`)
	}))
	mux.HandleFunc("DELETE /pty/process", func(w http.ResponseWriter, r *http.Request) {
		require.True(t, exited.Load(), "deletion must follow actual exit observation")
		deleted.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- client.Terminate(ctx, "process") }()
	select {
	case <-signaled:
	case <-time.After(5 * time.Second):
		t.Fatal("termination signal was not delivered")
	}
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	require.False(t, deleted.Load())
	exited.Store(true)
	require.NoError(t, client.Terminate(t.Context(), "process"))
	require.True(t, deleted.Load())
	require.NoError(t, client.Terminate(t.Context(), "process"))
	require.EqualValues(t, 2, signals.Load())
}

func TestTerminateDoesNotLaunchReservationsOrDeleteUncertainProcesses(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantDelete bool
		wantError  bool
	}{
		{"unstarted-or-exited", `{"session_id":"process","running":false}`, 200, true, false},
		{"absent", "", 404, false, false},
		{"provider-unavailable", "sensitive provider diagnostic", 503, false, true},
		{"wrong-process", `{"session_id":"other","running":false}`, 200, false, true},
		{"missing-state", `{"session_id":"process"}`, 200, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var deleted atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deleted.Store(true)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				require.Equal(t, "/pty/process", r.URL.Path, "a reserved process must not be attached and launched")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := execd.NewClient(server.Client(), server.URL, "test-only")
			require.NoError(t, err)
			err = client.Terminate(t.Context(), "process")
			if test.wantError {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "sensitive provider diagnostic")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, test.wantDelete, deleted.Load())
		})
	}
}

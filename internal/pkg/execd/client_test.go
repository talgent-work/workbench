package execd_test

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"golang.org/x/net/websocket"
)

func TestPipePreservesStreamIdentityAndRejectsMalformedExit(t *testing.T) {
	mux := http.NewServeMux()
	mux.Handle("GET /pty/process/ws", websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		require.Equal(t, "pty=0&since=0", ws.Request().URL.RawQuery)
		require.Equal(t, "test-only", ws.Request().Header.Get("X-EXECD-ACCESS-TOKEN"))
		require.NoError(t, websocket.Message.Send(ws, []byte{1, 'a'}))
		require.NoError(t, websocket.Message.Send(ws, []byte{2, 'b'}))
		replay := make([]byte, 10)
		replay[0], replay[9] = 3, 'c'
		binary.BigEndian.PutUint64(replay[1:9], 42)
		require.NoError(t, websocket.Message.Send(ws, replay))
		require.NoError(t, websocket.Message.Send(ws, `{"type":"exit"}`))
	}))
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pipe, err := client.Attach(ctx, "process", 0)
	require.NoError(t, err)
	defer pipe.Close()
	for i, kind := range []string{"stdout", "stderr", "replay"} {
		frame, err := pipe.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, kind, frame.Type)
		require.Equal(t, []byte{byte('a' + i)}, frame.Data)
		if kind == "replay" {
			require.Equal(t, int64(42), frame.Offset)
		}
	}
	_, err = pipe.Receive(ctx)
	require.ErrorContains(t, err, "missing exit code")
}

func TestReceiveCancellationDetachesWithoutReportingExit(t *testing.T) {
	closed := make(chan struct{})
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer close(closed)
		defer ws.Close()
		var b []byte
		_ = websocket.Message.Receive(ws, &b)
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	pipe, err := client.Attach(context.Background(), "process", 0)
	require.NoError(t, err)
	defer pipe.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = pipe.Receive(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("peer did not observe disconnect")
	}
}

// Cancellation during the dial stays recognizable, as it does once attached.
func TestAttachCancellationIsReportedAsCancellation(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) { _ = ws.Close() }))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Attach(ctx, "process", 0)
	require.ErrorIs(t, err, context.Canceled)
}

func TestCredentialsDoNotFollowRedirectAndProviderBodyIsNotReturned(t *testing.T) {
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed.Store(true) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
		_, _ = w.Write([]byte("private-command-output"))
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	_, err = client.Create(context.Background(), "/workspace", "claude")
	require.ErrorContains(t, err, "HTTP 307")
	require.NotContains(t, err.Error(), "private-command-output")
	require.False(t, followed.Load())
	_, err = client.Attach(context.Background(), "../other", 0)
	require.Error(t, err)
}

// Launch commands carry plugin parameter values, Sensitive ones included, and
// are stored unencrypted; a failed start must not repeat them.
func TestFailedCreateDoesNotReportTheCommand(t *testing.T) {
	const command = "claude --plugin-config token=sensitive-parameter-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write(body)
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	_, err = client.Create(context.Background(), "/workspace", command)
	require.ErrorContains(t, err, "HTTP 400")
	require.NotContains(t, err.Error(), "sensitive-parameter-value")
}

func TestPipeRejectsConnectionToAnotherProcess(t *testing.T) {
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		require.NoError(t, websocket.Message.Send(ws, `{"type":"connected","mode":"pipe","role":"holder","session_id":"other-process"}`))
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pipe, err := client.Attach(ctx, "expected-process", 0)
	require.NoError(t, err)
	defer pipe.Close()
	_, err = pipe.Receive(ctx)
	require.ErrorContains(t, err, "ownership mismatch")
}

func TestProcessDeletionOnlyTreatsNotFoundAsSuccessfulAbsence(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "/pty/preparation", r.URL.Path)
				require.Equal(t, "DELETE", r.Method)
				require.Equal(t, "test-only", r.Header.Get("X-EXECD-ACCESS-TOKEN"))
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-diagnostic"))
			}))
			defer server.Close()
			client, err := execd.NewClient(server.Client(), server.URL, "test-only")
			require.NoError(t, err)
			err = client.Delete(t.Context(), "preparation")
			if status == http.StatusNoContent || status == http.StatusNotFound {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private-diagnostic")
			}
		})
	}
}

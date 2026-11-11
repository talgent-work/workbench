package execd_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
)

// execd starts about a second after its sandbox: until then its address
// refuses connections.
func TestAwaitReadyWaitsForAnExecdThatStartsLate(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := reserved.Addr().String()
	require.NoError(t, reserved.Close())

	var pings atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/ping" && r.Header.Get("X-EXECD-ACCESS-TOKEN") == "test-only" {
			pings.Add(1)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	started := make(chan struct{})
	time.AfterFunc(400*time.Millisecond, func() {
		defer close(started)
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return
		}
		server.Listener = listener
		server.Start()
	})
	t.Cleanup(func() { <-started; server.Close() })

	client, err := execd.NewClient(http.DefaultClient, "http://"+address, "test-only")
	require.NoError(t, err)
	_, err = client.Create(context.Background(), "/workspace", "true")
	require.ErrorContains(t, err, "connection refused", "the first call races execd's start")

	require.NoError(t, client.AwaitReady(context.Background(), 5*time.Second))
	require.Equal(t, int32(1), pings.Load(), "ready once execd answers its ping")
}

func TestAwaitReadyEndsAtItsDeadlineNamingTheCause(t *testing.T) {
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := reserved.Addr().String()
	require.NoError(t, reserved.Close())
	client, err := execd.NewClient(http.DefaultClient, "http://"+address, "test-only")
	require.NoError(t, err)

	begun := time.Now()
	err = client.AwaitReady(context.Background(), 500*time.Millisecond)
	require.ErrorIs(t, err, execd.ErrNotReady)
	require.NotErrorIs(t, err, context.DeadlineExceeded, "its own deadline is not the caller's")
	require.ErrorContains(t, err, "connection refused")
	require.Less(t, time.Since(begun), 2*time.Second)
}

// A refusal of the request itself, such as a wrong access token, is not a
// late start: waiting cannot change it.
func TestAwaitReadyDoesNotWaitOutARefusal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)

	begun := time.Now()
	err = client.AwaitReady(context.Background(), 5*time.Second)
	require.ErrorContains(t, err, "HTTP 401")
	require.Less(t, time.Since(begun), time.Second)
}

func TestAwaitReadyStopsWithItsCaller(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "test-only")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err = client.AwaitReady(ctx, 5*time.Second)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

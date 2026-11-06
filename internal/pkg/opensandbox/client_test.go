package opensandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLifecycleClientDoesNotRedirectCredentials(t *testing.T) {
	var forwarded atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(401) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OPEN-SANDBOX-API-KEY") != "private-key" {
			w.WriteHeader(401)
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	original := source.Client()
	client, err := NewLifecycleClient(original, Config{Address: source.URL, APIKey: "private-key"})
	require.NoError(t, err)
	require.Nil(t, original.CheckRedirect, "shared HTTP policy must not be mutated")
	_, err = client.GetSandbox(context.Background(), "sandbox")
	require.Error(t, err)
	require.Zero(t, forwarded.Load())
}

func TestEndpointResolutionCancelsProviderRequest(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	client, err := NewLifecycleClient(server.Client(), Config{Address: server.URL, APIKey: "private-key"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := client.GetEndpoint(ctx, "sandbox", 44772, nil); result <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("endpoint request did not arrive")
	}
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("endpoint caller did not cancel")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("provider request outlived caller cancellation")
	}
}

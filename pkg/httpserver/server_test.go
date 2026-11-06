package httpserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"gopkg.in/yaml.v3"

	"github.com/talgent-work/workbench/pkg/httpserver"
)

func TestConfigYAMLUnmarshal(t *testing.T) {
	raw := []byte("addr: \":9090\"\n")

	var cfg httpserver.Config
	require.NoError(t, yaml.Unmarshal(raw, &cfg))
	require.Equal(t, ":9090", cfg.Addr)
}

func TestConfigJSONUnmarshal(t *testing.T) {
	raw := []byte(`{"addr":":9090"}`)

	var cfg httpserver.Config
	require.NoError(t, json.Unmarshal(raw, &cfg))
	require.Equal(t, ":9090", cfg.Addr)
}

func TestNewUsesConfiguredAddressAndRegistersLifecycle(t *testing.T) {
	lc := &fxLifecycle{}
	mux := http.NewServeMux()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := httpserver.New(lc, httpserver.Config{Addr: ":9090"}, mux, logger)

	require.Equal(t, ":9090", srv.Addr)
	require.NotNil(t, srv.Handler)
	require.Len(t, lc.hooks, 1)
	require.NotNil(t, lc.hooks[0].OnStart)
	require.NotNil(t, lc.hooks[0].OnStop)
}

func TestNewStartsHTTPServerAndShutsDown(t *testing.T) {
	addr := freeLocalAddr(t)
	lc := &fxLifecycle{}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	httpserver.New(lc, httpserver.Config{Addr: addr}, mux, logger)
	require.Len(t, lc.hooks, 1)
	require.NoError(t, lc.hooks[0].OnStart(context.Background()))

	resp, err := http.Get("http://" + addr + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "ok", string(body))

	require.NoError(t, lc.hooks[0].OnStop(context.Background()))
}

// Shutdown must not wait for a stream whose client never leaves, and must
// still let an in-flight unary request finish.
func TestShutdownEndsStreamsAndDrainsUnaryRequests(t *testing.T) {
	addr := freeLocalAddr(t)
	lc := &fxLifecycle{}
	mux := http.NewServeMux()
	streaming, unary := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	mux.HandleFunc("/stream", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(streaming)
		<-r.Context().Done()
	})
	mux.HandleFunc("/unary", func(w http.ResponseWriter, r *http.Request) {
		close(unary)
		<-release
		if r.Context().Err() != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("done"))
	})
	httpserver.New(lc, httpserver.Config{Addr: addr}, mux, slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, lc.hooks[0].OnStart(context.Background()))

	stream, err := http.NewRequest(http.MethodPost, "http://"+addr+"/stream", strings.NewReader("{}"))
	require.NoError(t, err)
	stream.Header.Set("Content-Type", "application/connect+json")
	go func() {
		if resp, err := http.DefaultClient.Do(stream); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-streaming
	unaryResult := make(chan string, 1)
	go func() {
		resp, err := http.Post("http://"+addr+"/unary", "application/json", strings.NewReader("{}"))
		if err != nil {
			unaryResult <- err.Error()
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		unaryResult <- string(body)
	}()
	<-unary

	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stopped <- lc.hooks[0].OnStop(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Shutdown waited for an open stream")
	}
	require.Equal(t, "done", <-unaryResult)
}

func freeLocalAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	return ln.Addr().String()
}

type fxLifecycle struct {
	hooks []fx.Hook
}

func (l *fxLifecycle) Append(h fx.Hook) {
	l.hooks = append(l.hooks, h)
}

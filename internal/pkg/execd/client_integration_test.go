//go:build integration

package execd_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// Run with EXECD_BINARY pointing at the actual OpenSandbox execd executable.
// The remote child is controlled test input; HTTP, WebSocket and process I/O are real.
func TestNativeExecdPipeProcess(t *testing.T) {
	binary := nativetest.Require(t, "EXECD_BINARY")[0]
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := reservation.Addr().(*net.TCPAddr).Port
	require.NoError(t, reservation.Close())
	root := t.TempDir()
	log, err := os.Create(filepath.Join(root, "execd.log"))
	require.NoError(t, err)
	defer log.Close()
	daemon := exec.Command(binary, fmt.Sprintf("--port=%d", port))
	daemon.Dir = root
	daemon.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "EXECD_ACCESS_TOKEN=integration-only"}
	daemon.Stdout, daemon.Stderr = log, log
	require.NoError(t, daemon.Start())
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	address := fmt.Sprintf("http://127.0.0.1:%d", port)
	httpClient := &http.Client{Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	require.Eventually(t, func() bool {
		r, _ := http.NewRequestWithContext(ctx, http.MethodGet, address+"/ping", nil)
		r.Header.Set("X-EXECD-ACCESS-TOKEN", "integration-only")
		response, err := httpClient.Do(r)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	}, 5*time.Second, 20*time.Millisecond)
	wrong, err := execd.NewClient(httpClient, address, "wrong")
	require.NoError(t, err)
	_, err = wrong.Create(ctx, root, "true")
	require.ErrorContains(t, err, "HTTP 401")
	client, err := execd.NewClient(httpClient, address, "integration-only")
	require.NoError(t, err)
	// The process waits for SIGINT by sleeping in short steps, not with
	// signal.pause(): a signal arriving after Python ran its pending handlers
	// and before pause() blocks is never seen, which a loaded runner hits.
	command := `exec python3 -u -c 'import pathlib,signal,sys,time; pathlib.Path("started").touch(); signal.signal(signal.SIGINT,lambda *_: sys.exit(0)); print("READY",flush=True); line=sys.stdin.readline(); print("OUT:"+line.strip(),flush=True); print("ERR:"+line.strip(),file=sys.stderr,flush=True)
while True: time.sleep(0.05)'`
	id, err := client.Create(ctx, root, command)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = client.Delete(cleanup, id)
	})
	pipe, err := client.Attach(ctx, id, 0)
	require.NoError(t, err)
	defer pipe.Close()
	var stdout, stderr string
	connected := false
	for !connected || !strings.Contains(stdout, "READY") {
		frame, err := pipe.Receive(ctx)
		require.NoError(t, err)
		switch frame.Type {
		case "connected":
			connected = true
			require.Equal(t, id, frame.SessionID)
		case "stdout", "replay":
			stdout += string(frame.Data)
		case "exit":
			t.Fatal("process exited before input")
		}
	}
	require.NoError(t, pipe.WriteInput(ctx, []byte("native-input\n")))
	for !strings.Contains(stdout, "OUT:native-input") || !strings.Contains(stderr, "ERR:native-input") {
		frame, err := pipe.Receive(ctx)
		require.NoError(t, err)
		switch frame.Type {
		case "stdout":
			stdout += string(frame.Data)
		case "stderr":
			stderr += string(frame.Data)
		case "exit":
			t.Fatal("process exited before output")
		}
	}
	require.NoError(t, pipe.Signal(ctx, "SIGINT"))
	for {
		frame, err := pipe.Receive(ctx)
		require.NoError(t, err)
		if frame.Type == "exit" {
			require.Equal(t, 0, *frame.ExitCode)
			break
		}
	}
	require.NoError(t, client.Delete(ctx, id))
	require.NoError(t, client.Delete(ctx, id), "recovery must tolerate an already deleted preparation handle")
}

// The stdin response is a barrier after signal delivery: a duplicate signal
// would change the count before the child handles the following input.

//go:build integration

package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// Real execd and native Claude validate the launch/reattachment boundary. No
// inference, Agent feedback or Kubernetes lifecycle is claimed by this test.
func TestNativeLaunchInitializesClaude(t *testing.T) {
	for _, mode := range []domain.ContextMode{domain.ContextFresh, domain.ContextRecover} {
		t.Run(string(mode), func(t *testing.T) {
			required := nativetest.Require(t, "EXECD_BINARY", "CLAUDE_BINARY")
			execdBinary, claudeBinary := required[0], required[1]
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			root := t.TempDir()
			reservation, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			port := reservation.Addr().(*net.TCPAddr).Port
			require.NoError(t, reservation.Close())
			log, err := os.Create(filepath.Join(root, "execd.log"))
			require.NoError(t, err)
			defer log.Close()
			daemon := exec.Command(execdBinary, fmt.Sprintf("--port=%d", port))
			daemon.Dir = root
			daemon.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "CLAUDE_CODE_PROJECT_DIR_NAME=agent-native-test", "EXECD_ACCESS_TOKEN=execd-test", "ANTHROPIC_API_KEY=local-test-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "TALGENT_WORK_ID=fixture", "TALGENT_WORK_CREDENTIAL=fixture-credential"}
			daemon.Stdout, daemon.Stderr = log, log
			require.NoError(t, daemon.Start())
			t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
			endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
			require.Eventually(t, func() bool {
				req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"/ping", nil)
				req.Header.Set("X-EXECD-ACCESS-TOKEN", "execd-test")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return false
				}
				defer resp.Body.Close()
				return resp.StatusCode == 200
			}, 5*time.Second, 20*time.Millisecond)
			client, err := execd.NewClient(http.DefaultClient, endpoint, "execd-test")
			require.NoError(t, err)
			input := request()
			input.WorkID = uuid.NewString()
			input.ContextMode = mode
			launch, err := (NativeLaunchConfig{Binary: claudeBinary}).prepare(sandboxLayout, input, owner(input), "")
			require.NoError(t, err)
			marker := "/tmp/talgent-native-" + storageKey(input.WorkID+":"+fixtureAllocationID)
			t.Cleanup(func() { _ = os.Remove(marker); _ = os.Remove(marker + ".stderr") })
			handle, err := client.Create(ctx, root, launch.Command)
			require.NoError(t, err)
			t.Cleanup(func() {
				cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
				defer done()
				_ = client.Delete(cleanup, handle)
			})
			pipe, err := client.Attach(ctx, handle, 0)
			require.NoError(t, err)
			defer pipe.Close()
			require.NoError(t, pipe.WriteInput(ctx, []byte("{\"type\":\"control_request\",\"request_id\":\"initialize-test\",\"request\":{\"subtype\":\"initialize\"}}\n")))
			var pending []byte
			initialized := false
			for !initialized {
				frame, err := pipe.Receive(ctx)
				require.NoError(t, err)
				require.NotEqual(t, "exit", frame.Type, "native command exited before initialization")
				if frame.Type != "stdout" {
					continue
				}
				pending = append(pending, frame.Data...)
				for {
					end := bytes.IndexByte(pending, '\n')
					if end < 0 {
						break
					}
					var message struct {
						Type     string
						Response struct {
							Subtype   string
							RequestID string `json:"request_id"`
						}
					}
					require.NoError(t, json.Unmarshal(pending[:end], &message))
					pending = pending[end+1:]
					if message.Type == "control_response" && message.Response.RequestID == "initialize-test" {
						require.Equal(t, "success", message.Response.Subtype)
						initialized = true
					}
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
			require.NoError(t, pipe.Close())
		})
	}
}

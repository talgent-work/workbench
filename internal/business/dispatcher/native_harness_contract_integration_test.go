//go:build integration

package dispatcher_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

// harnessProcess is one headless Harness speaking stream-json on stdio.
type harnessProcess struct {
	t       *testing.T
	session string
	encoder *json.Encoder
	events  chan map[string]any
}

func startHarnessAt(t *testing.T, ctx context.Context, model *nativeInference, root string) *harnessProcess {
	t.Helper()
	binary := nativetest.Require(t, "CLAUDE_BINARY")[0]
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	session := uuid.NewString()
	process := exec.CommandContext(ctx, binary, "-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--replay-user-messages", "--permission-prompt-tool", "stdio", "--permission-mode", "bypassPermissions", "--session-id", session)
	process.Dir = root
	process.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "ANTHROPIC_API_KEY=local-test-only", "ANTHROPIC_BASE_URL=" + server.URL, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	input, err := process.StdinPipe()
	require.NoError(t, err)
	output, err := process.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, process.Start())
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	h := &harnessProcess{t: t, session: session, encoder: json.NewEncoder(input), events: make(chan map[string]any, 256)}
	go func() {
		defer close(h.events)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 16<<20)
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				h.events <- event
			}
		}
	}()
	h.write(map[string]any{"type": "control_request", "request_id": "initialize", "request": map[string]any{"subtype": "initialize"}})
	h.until(ctx, func(event map[string]any) bool { return event["type"] == "control_response" })
	return h
}

func (h *harnessProcess) write(value any) {
	h.t.Helper()
	require.NoError(h.t, h.encoder.Encode(value))
}

func (h *harnessProcess) input(id, text string) {
	h.t.Helper()
	h.write(map[string]any{"type": "user", "uuid": id, "session_id": h.session, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": text}})
}

// until returns the events read up to and including the first matching one.
func (h *harnessProcess) until(ctx context.Context, match func(map[string]any) bool) []map[string]any {
	h.t.Helper()
	var seen []map[string]any
	for {
		select {
		case event, ok := <-h.events:
			if !ok {
				h.t.Fatalf("Harness output ended; read %v", seen)
			}
			seen = append(seen, event)
			if match(event) {
				return seen
			}
		case <-ctx.Done():
			h.t.Fatalf("timed out; read %v", seen)
		}
	}
}

func rootResult(event map[string]any) bool {
	return event["type"] == "result" && event["parent_tool_use_id"] == nil
}

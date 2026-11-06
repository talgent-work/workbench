//go:build integration

package dispatcher_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/pkg/nativetest"
)

const nativePrivateWork = "session-test"

// nativeExecutionFixture owns isolated native processes and the private API.
// Its controls always follow the current Dispatcher replica after a handover.
type nativeExecutionFixture struct {
	ctx                      context.Context
	model                    *nativeInference
	root, endpoint, nativeID string
	processClient            *execd.Client
	records                  *nativeRecords
	targets                  map[string]*dispatcherprivatev1.ConnectRequest
	manager                  *dispatcher.NativeExecutions
	client                   dispatcherprivatev1connect.ExecutionServiceClient
	runtime                  func() (*dispatcher.NativeExecutions, dispatcherprivatev1connect.ExecutionServiceClient)
	connectProcess, start    func(string, bool) string
	connect                  func(*dispatcherprivatev1.ConnectRequest)
	enqueue                  func(*dispatcherprivatev1.EnqueueInputRequest) (dispatcherprivatev1.InputHandOff, error)
	send                     func(string, string) *dispatcherprivatev1.BufferedText
	respond                  func(*dispatcherprivatev1.BufferedReply)
	stop                     func(string)
}

func newNativeExecutionFixture(t *testing.T) *nativeExecutionFixture {
	t.Helper()
	f := &nativeExecutionFixture{}
	var err error
	var cancel context.CancelFunc
	program := os.Getenv(talgentComputerProgram)
	var execdBinary, claudeBinary string
	if program != "" {
		claudeBinary = nativetest.Require(t, "CLAUDE_BINARY")[0]
	} else {
		required := nativetest.Require(t, "EXECD_BINARY", "CLAUDE_BINARY")
		execdBinary, claudeBinary = required[0], required[1]
	}
	f.ctx, cancel = context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	modelName := os.Getenv("NATIVE_TEST_MODEL")
	if modelName == "" {
		modelName = "claude-sonnet-4-6"
	}
	f.model = &nativeInference{}
	inference := httptest.NewServer(f.model)
	t.Cleanup(inference.Close)
	f.root = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(f.root, ".claude"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(f.root, ".claude", "settings.json"), []byte(`{"env":{"NATIVE_SETTING_SENTINEL":"settings-only-secret"}}`), 0600))
	daemonEnv := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.root, "CLAUDE_CONFIG_DIR=" + filepath.Join(f.root, ".claude"), "CLAUDE_CODE_PROJECT_DIR_NAME=dispatcher-test", "EXECD_ACCESS_TOKEN=execd-test", "ANTHROPIC_API_KEY=local-test-only", "CLAUDE_CODE_MAX_RETRIES=0", "ANTHROPIC_BASE_URL=" + inference.URL, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "ANTHROPIC_MODEL=" + modelName, "CLAUDE_CODE_EFFORT_LEVEL=max"}
	if program != "" {
		f.endpoint = startTalgentComputer(t, f.ctx, program, claudeBinary, f.root, daemonEnv, "execd-test")
		claudeBinary = managedHarness
	} else {
		reservation, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		port := reservation.Addr().(*net.TCPAddr).Port
		require.NoError(t, reservation.Close())
		log, err := os.Create(filepath.Join(f.root, "execd.log"))
		require.NoError(t, err)
		t.Cleanup(func() { _ = log.Close() })
		daemon := exec.Command(execdBinary, fmt.Sprintf("--port=%d", port))
		daemon.Dir = f.root
		daemon.Env = daemonEnv
		daemon.Stdout, daemon.Stderr = log, log
		require.NoError(t, daemon.Start())
		t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
		f.endpoint = fmt.Sprintf("http://127.0.0.1:%d", port)
		require.Eventually(t, func() bool {
			r, _ := http.NewRequestWithContext(f.ctx, "GET", f.endpoint+"/ping", nil)
			r.Header.Set("X-EXECD-ACCESS-TOKEN", "execd-test")
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				return false
			}
			defer resp.Body.Close()
			return resp.StatusCode == 200
		}, 5*time.Second, 20*time.Millisecond)
	}
	f.processClient, err = execd.NewClient(http.DefaultClient, f.endpoint, "execd-test")
	require.NoError(t, err)
	f.records = &nativeRecords{}
	delivery := newTestDelivery(t, f.records, f.records)
	replicas := newNativeReplicas(t)
	runtimes := 0
	f.runtime = func() (*dispatcher.NativeExecutions, dispatcherprivatev1connect.ExecutionServiceClient) {
		t.Helper()
		runtimes++
		instance := fmt.Sprintf("runtime-%d", runtimes)
		f.manager = replicas.replica(instance, f.records, delivery)
		_, f.client = privateAPI(t, f.manager, replicas.ownership(instance, "127.0.0.1:0"), replicas.inputs())
		return f.manager, f.client
	}
	f.manager, f.client = f.runtime()
	f.targets = make(map[string]*dispatcherprivatev1.ConnectRequest)
	f.nativeID = uuid.NewString()
	// Every process run below belongs to one Work. A run label only marks where
	// that run's feedback starts; feedback itself carries no run identity.
	// The exit fact reaches Agent before Dispatcher releases the ended stream,
	// so a Connect for the next process can briefly conflict with it. Agent
	// redelivers such a Connect, as this retry does.
	f.connect = func(request *dispatcherprivatev1.ConnectRequest) {
		t.Helper()
		require.Eventually(t, func() bool {
			_, err := f.client.Connect(f.ctx, nativeRequest(request))
			if connect.CodeOf(err) == connect.CodeFailedPrecondition {
				return false
			}
			require.NoError(t, err)
			return true
		}, 10*time.Second, 20*time.Millisecond, "the ended stream is released")
	}
	f.connectProcess = func(executionID string, resume bool) string {
		t.Helper()
		flag := "--session-id"
		if resume {
			flag = "--resume"
		}
		diagnostic := strings.ReplaceAll(filepath.Join(f.root, executionID+".stderr"), "'", "'\"'\"'")
		processID, err := f.processClient.Create(f.ctx, f.root, "exec 2>>'"+diagnostic+"'; unset CLAUDE_CODE_EFFORT_LEVEL; exec '"+strings.ReplaceAll(claudeBinary, "'", "'\"'\"'")+"' -p --effort max --input-format stream-json --output-format stream-json --verbose --replay-user-messages --permission-prompt-tool stdio --permission-mode bypassPermissions "+flag+" "+f.nativeID)
		require.NoError(t, err)
		t.Cleanup(func() {
			c, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			_ = f.processClient.Delete(c, processID)
		})
		f.records.mark(executionID, processID)
		request := &dispatcherprivatev1.ConnectRequest{WorkId: nativePrivateWork, Endpoint: f.endpoint, AccessToken: "execd-test", ProcessId: processID, NativeSessionId: f.nativeID}
		f.targets[executionID] = request
		_, err = f.client.Connect(f.ctx, connect.NewRequest(request))
		require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		f.connect(request)
		_, err = f.client.Connect(f.ctx, nativeRequest(request))
		require.NoError(t, err)
		return executionID
	}
	f.start = func(executionID string, resume bool) string {
		t.Helper()
		ref := f.connectProcess(executionID, resume)
		f.records.await(t, executionID, command.ExecutionStarted)
		return ref
	}
	f.enqueue = func(entry *dispatcherprivatev1.EnqueueInputRequest) (dispatcherprivatev1.InputHandOff, error) {
		entry.WorkId = nativePrivateWork
		response, err := f.client.EnqueueInput(f.ctx, nativeRequest(entry))
		if err != nil {
			return 0, err
		}
		return response.Msg.GetHandOff(), nil
	}
	f.send = func(_ string, text string) *dispatcherprivatev1.BufferedText {
		t.Helper()
		input := &dispatcherprivatev1.BufferedText{InputId: uuid.NewString(), Text: text}
		handOff, err := f.enqueue(&dispatcherprivatev1.EnqueueInputRequest{Entry: &dispatcherprivatev1.EnqueueInputRequest_Text{Text: input}})
		require.NoError(t, err)
		require.Equal(t, dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_BUFFERED, handOff)
		return input
	}
	f.respond = func(reply *dispatcherprivatev1.BufferedReply) {
		t.Helper()
		_, err := f.enqueue(&dispatcherprivatev1.EnqueueInputRequest{Entry: &dispatcherprivatev1.EnqueueInputRequest_Reply{Reply: reply}})
		require.NoError(t, err)
	}
	// A stop is refused while a Turn runs; Agent redelivers it until the
	// process exits, as this retry does.
	f.stop = func(ref string) {
		t.Helper()
		stopID := uuid.NewString()
		require.Eventually(t, func() bool {
			_, err := f.client.Stop(f.ctx, nativeRequest(&dispatcherprivatev1.StopRequest{WorkId: nativePrivateWork, StopRequestId: stopID}))
			if connect.CodeOf(err) == connect.CodeFailedPrecondition {
				return false
			}
			require.NoError(t, err)
			return true
		}, 30*time.Second, 50*time.Millisecond, "the stop proceeds once the Turn ends")
		observation := f.records.await(t, ref, command.ExecutionExited)
		require.Equal(t, f.targets[ref].ProcessId, observation.ProcessID, "the exit fact names its process")
		require.Empty(t, observation.ExitReason, "a stopped process exits as asked")
	}
	return f
}

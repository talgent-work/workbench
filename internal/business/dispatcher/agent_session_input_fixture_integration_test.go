//go:build integration

package dispatcher_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/pkg/mysqltest"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	agentcommand "github.com/talgent-work/workbench/internal/business/agent/application/command"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"github.com/talgent-work/workbench/pkg/authn"
	"xorm.io/xorm"
)

type nativeMessageCapacity struct{}

func (nativeMessageCapacity) AdmitRoom(context.Context) error { return nil }

type nativeMessageResources struct {
	client                 *execd.Client
	dispatcher             dispatcherprivatev1connect.ExecutionServiceClient
	binary, root, endpoint string
	mu                     sync.Mutex
	processIDs             []string
	current                map[string]string
	reclaimed              map[string]bool
}

func (r *nativeMessageResources) Create(ctx context.Context, input agentdomain.StartExecutionRequest, _ agentdomain.SandboxMounts) error {
	binary := "'" + strings.ReplaceAll(r.binary, "'", "'\"'\"'") + "'"
	processID, err := r.client.Create(ctx, r.root, "exec "+binary+" -p --input-format stream-json --output-format stream-json --verbose --replay-user-messages --permission-prompt-tool stdio --permission-mode bypassPermissions --session-id "+input.HarnessSessionReference)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.processIDs = append(r.processIDs, processID)
	if r.current == nil {
		r.current = map[string]string{}
	}
	r.current[input.WorkID] = processID
	r.mu.Unlock()
	_, err = r.dispatcher.Connect(ctx, nativeRequest(&dispatcherprivatev1.ConnectRequest{WorkId: input.WorkID, Endpoint: r.endpoint, AccessToken: "execd-test", ProcessId: processID, NativeSessionId: input.HarnessSessionReference}))
	return err
}

// CreateManaged is never reached: the fixture places every Work on a Cloud.
func (r *nativeMessageResources) CreateManaged(context.Context, agentdomain.StartExecutionRequest) error {
	return errors.New("fixture: no Managed Computer")
}

func (*nativeMessageResources) RetireFenced(context.Context, string) error {
	return errors.New("fixture: no Managed Computer")
}

func (r *nativeMessageResources) Delete(_ context.Context, workID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reclaimed == nil {
		r.reclaimed = map[string]bool{}
	}
	r.reclaimed[workID] = true
	return nil
}

func (*nativeMessageResources) StartRejected(context.Context, string) (bool, error) {
	return false, nil
}
func (*nativeMessageResources) StartAbandoned(context.Context, string) (bool, error) {
	return false, nil
}

func (r *nativeMessageResources) process(workID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current[workID]
}

// nativeProcessObservation answers Agent's reconciliation from the memory of
// the Dispatcher replica reading the stream, as the production adapter does.
type nativeProcessObservation struct {
	manager   *dispatcher.NativeExecutions
	resources *nativeMessageResources
}

func (o nativeProcessObservation) ObserveProcess(ctx context.Context, workID string) (agentcommand.WorkProcessObservation, error) {
	o.resources.mu.Lock()
	reclaimed := o.resources.reclaimed[workID]
	o.resources.mu.Unlock()
	if reclaimed {
		return agentcommand.WorkProcessObservation{ProcessObservation: agentdomain.ProcessObservation{State: agentdomain.ProcessPending, ComputeReclaimed: true}}, nil
	}
	observed, err := o.manager.ObserveProcess(ctx, workID, o.resources.process(workID))
	if err != nil {
		return agentcommand.WorkProcessObservation{}, err
	}
	return agentcommand.WorkProcessObservation{
		ProcessObservation: agentdomain.ProcessObservation{State: agentdomain.ProcessState(observed.State), Ready: observed.Ready},
		Turn:               agentdomain.TurnObservation{Sequence: observed.Turn.Sequence, Active: observed.Turn.Active, Completed: observed.Turn.Completed},
		PhaseSequence:      observed.PhaseSequence, RequiredInteractionIDs: observed.RequiredInteractionIDs,
	}, nil
}

func (o nativeProcessObservation) IsCurrentProcess(_ context.Context, workID, processID string) (bool, error) {
	return processID != "" && processID == o.resources.process(workID), nil
}

func (o nativeProcessObservation) AcknowledgeExit(ctx context.Context, workID, processID string) (bool, error) {
	return o.IsCurrentProcess(ctx, workID, processID)
}

// agentWorkDB holds the Agent tables of this end-to-end fixture.
func agentWorkDB(t *testing.T) *xorm.Engine {
	t.Helper()
	ctx := context.Background()
	container, err := mysqltest.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Release(ctx) })
	dsn, err := container.ConnectionString(ctx, "parseTime=true&multiStatements=true")
	require.NoError(t, err)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

type nativeSessionFeedback struct {
	*nativeRecords
	target *agentfeedback.Client
}

func (r *nativeSessionFeedback) ReportExecution(ctx context.Context, observation command.ExecutionObservation) error {
	if err := r.target.ReportExecution(ctx, observation); err != nil {
		return err
	}
	return r.nativeRecords.ReportExecution(ctx, observation)
}

// No plugin selection is configured by this messaging scenario; workspace
// Skills are loaded by the real Harness from its own project configuration.
type nativeMessageCatalog struct{}

func (nativeMessageCatalog) ListPersonalPlugins(context.Context, string) ([]agentdomain.PersonalPlugin, error) {
	return nil, nil
}

func (nativeMessageCatalog) ResolvePlugin(context.Context, string, string) (agentdomain.PluginRelease, error) {
	panic("messaging fixture has no Plugin selections")
}

func (nativeMessageCatalog) ReadConnectorSelection(context.Context, string) (agentcommand.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}

func assertNativeWorkCapabilities(t *testing.T, ctx context.Context, client agentpublicv1connect.AgentWorkServiceClient, workID string) {
	t.Helper()
	request := connect.NewRequest(&agentpublicv1.GetWorkCapabilitiesRequest{WorkId: workID})
	request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
	request.Header().Set(authn.HeaderSubject, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	response, err := client.GetWorkCapabilities(ctx, request)
	require.NoError(t, err)
	require.True(t, response.Msg.Observed)
	var skill, compact bool
	for _, command := range response.Msg.Commands {
		switch command.Name {
		case "entry":
			skill = true
			require.Contains(t, command.Description, "Native entry")
			require.Equal(t, "[value]", command.ArgumentHint)
		case "compact":
			compact = true
		}
	}
	require.True(t, skill, "public candidates include the actual workspace Skill")
	require.True(t, compact, "public candidates include native commands")
}

func nativeMessagingExecd(t *testing.T, ctx context.Context, root, execdBinary, claudeBinary, program, inferenceURL string) (*execd.Client, string) {
	t.Helper()
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CLAUDE_CONFIG_DIR=" + filepath.Join(root, ".claude"), "EXECD_ACCESS_TOKEN=execd-test", "ANTHROPIC_API_KEY=local-test-only", "CLAUDE_CODE_MAX_RETRIES=0", "ANTHROPIC_MODEL=claude-sonnet-4-6", "ANTHROPIC_BASE_URL=" + inferenceURL, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	if program != "" {
		endpoint := startTalgentComputer(t, ctx, program, claudeBinary, root, env, "execd-test")
		processClient, err := execd.NewClient(http.DefaultClient, endpoint, "execd-test")
		require.NoError(t, err)
		return processClient, endpoint
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := reservation.Addr().(*net.TCPAddr).Port
	require.NoError(t, reservation.Close())
	logFile, err := os.Create(filepath.Join(root, "execd.log"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = logFile.Close() })
	daemon := exec.Command(execdBinary, fmt.Sprintf("--port=%d", port))
	daemon.Dir = root
	daemon.Env = env
	daemon.Stdout, daemon.Stderr = logFile, logFile
	require.NoError(t, daemon.Start())
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	require.Eventually(t, func() bool {
		req, _ := http.NewRequestWithContext(ctx, "GET", endpoint+"/ping", nil)
		req.Header.Set("X-EXECD-ACCESS-TOKEN", "execd-test")
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == 200
	}, 5*time.Second, 20*time.Millisecond)
	processClient, err := execd.NewClient(http.DefaultClient, endpoint, "execd-test")
	require.NoError(t, err)
	return processClient, endpoint
}

func (*nativeSessionFeedback) ReportConnectionEnded(context.Context, string, string) error {
	return nil
}

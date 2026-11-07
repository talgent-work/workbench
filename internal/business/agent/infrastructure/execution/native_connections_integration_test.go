//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/contracts/gen/go/dispatcher/private/v1"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/pluginconfig"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

type nativeConnectionReceiver struct {
	dispatcherprivatev1connect.UnimplementedExecutionServiceHandler
	provisioning *Provisioning
	requests     []*dispatcherprivatev1.ConnectRequest
	unavailable  bool
}

func (h *nativeConnectionReceiver) Connect(ctx context.Context, r *dispatcherprivatev1.ConnectRequest) (*dispatcherprivatev1.ConnectResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	if info.RequestHeader().Get("Authorization") != "Bearer dispatcher-secret" {
		return nil, connect.NewError(connect.CodeUnauthenticated, "invalid token")
	}
	record, found, err := h.provisioning.live(ctx, r.WorkId)
	if err != nil {
		return nil, err
	}
	if !record.SandboxReady || !record.EnvironmentPrepared {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "environment readiness was not committed before native connect")
	}
	if !found || record.ProcessID != r.ProcessId || record.NativeSessionID != r.NativeSessionId || record.SessionID != r.WorkId {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "process binding was not durably stored before connection")
	}
	h.requests = append(h.requests, r)
	if h.unavailable {
		return nil, connect.NewError(connect.CodeUnavailable, "Dispatcher unavailable")
	}
	return &dispatcherprivatev1.ConnectResponse{}, nil
}

func TestConnectionPersistsHandleBeforeRPCAndRetainsItOnRetry(t *testing.T) {
	ctx := context.Background()
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	provisioning := NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	attachNativeCredentials(t, db, provisioning, lifecycle)
	input := request()
	input.TraceAttributes = map[string]string{"talgent.case_id": "case-native-connection"}
	input.Capabilities, err = domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: []string{"fixed"}, DefaultStartupAgent: "fixed:reviewer"}, []domain.PluginRelease{{Slug: "fixed", Source: "fixture", Version: "1", RetrievalRef: "archive:fixed", NativeEntry: []byte(`{"name":"fixed","version":"1"}`), Agents: []domain.AgentComponent{{Name: "fixed:reviewer"}}, Parameters: pluginconfig.ParameterDeclarations{"paths": {Type: "string", Title: "Paths", Description: "Paths", Multiple: true, Default: []string{"src"}}}}})
	require.NoError(t, err)
	record, err := register(ctx, provisioning, input, testMounts)
	require.NoError(t, err)
	launchCommand, preparationCommand := record.LaunchCommand, withCheckoutPlan(record.PreparationCommand, []byte("null"))
	var spec struct{ Env map[string]string }
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	var reserved, preparations int
	processAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pty/preparation-handle/ws" {
			websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"preparation-handle","mode":"pipe","role":"holder"}`)
				_ = websocket.Message.Send(ws, append([]byte{1}, []byte(`{"results":[{"slug":"fixed","installed":true,"reason":""}]}`)...))
				_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
			}).ServeHTTP(w, r)
			return
		}
		if r.Method != "POST" || r.URL.Path != "/pty" || r.Header.Get("X-EXECD-ACCESS-TOKEN") != spec.Env["EXECD_ACCESS_TOKEN"] {
			w.WriteHeader(400)
			return
		}
		var request struct{ Cwd, Command string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Cwd != "/workspace" || (request.Command != launchCommand && request.Command != preparationCommand) {
			w.WriteHeader(400)
			return
		}
		if request.Command == preparationCommand {
			preparations++
			_, _ = w.Write([]byte(`{"session_id":"preparation-handle"}`))
			return
		}
		reserved++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"session_id":"process-handle"}`))
	}))
	defer processAPI.Close()
	lifecycle.endpoint = processAPI.URL
	receiver := &nativeConnectionReceiver{provisioning: provisioning, unavailable: true}
	mux := http.NewServeMux()
	path, handler := "/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, receiver) })
	mux.Handle(path, handler)
	dispatcher := httptest.NewServer(mux)
	defer dispatcher.Close()
	connections, err := NewNativeConnections(provisioning, provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret", command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	steps := newExecutionSteps(t, provisioning, connections, unread{}, nil, time.Now)
	pass := func() error { return steps.pass(ctx, db) }
	lifecycle.mu.Lock()
	lifecycle.createPending = true
	lifecycle.mu.Unlock()
	require.NoError(t, steps.reconcile(ctx, record.ID))
	require.Equal(t, sandboxID, current(t, steps, record.ID).Environment, "the creation is submitted and its sandbox bound")
	long, err := steps.handler.Reconcile(ctx, record.ID)
	require.NoError(t, err)
	require.False(t, long, "a pending sandbox is not prepared")
	require.NoError(t, pass())
	pending, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, sandboxID, pending.SandboxID)
	require.False(t, pending.SandboxReady, "allocation acceptance is not environment readiness")
	require.Zero(t, reserved, "pending Sandbox cannot reserve a native process")
	lifecycle.mu.Lock()
	lifecycle.info.Status.State = "Running"
	lifecycle.mu.Unlock()
	require.Error(t, pass(), "the first delivery, preparation and the Harness start, then an unavailable Dispatcher")
	recovering, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, recovering.SandboxReady)
	require.True(t, recovering.PreparationRecovering)
	require.Equal(t, 1, reserved)
	require.Len(t, receiver.requests, 1)
	require.Equal(t, input.WorkID, receiver.requests[0].GetWorkId(), "native projections are addressed to the admitted Work")
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	provisioning = NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "changed-binary"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	attachNativeCredentials(t, db, provisioning, lifecycle)
	receiver.provisioning = provisioning
	receiver.unavailable = false
	connections, err = NewNativeConnections(provisioning, provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret", command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	steps = newExecutionSteps(t, provisioning, connections, unread{}, nil, time.Now)
	require.NoError(t, pass())
	require.Equal(t, 1, reserved, "recovery must reuse the persisted reserved handle")
	require.Equal(t, 1, preparations, "installation must not repeat during Dispatcher reconnection")
	require.Len(t, receiver.requests, 2)
	require.Equal(t, receiver.requests[0], receiver.requests[1])
	after, found, err := latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, after.SandboxReady)
	require.False(t, after.PreparationRecovering)
	require.Equal(t, receiver.requests[0].ProcessId, after.ProcessID)
	require.Equal(t, record.LaunchCommand, after.LaunchCommand)
	require.NoError(t, pass())
	unchanged, _, err := latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, after.UpdatedAt, unchanged.UpdatedAt, "healthy observation must not rewrite the execution's record")
	require.Equal(t, after.Version, unchanged.Version)
	require.Len(t, receiver.requests, 3, "durable binding is offered again so Dispatcher can recover after restart")
	require.Equal(t, receiver.requests[1], receiver.requests[2])
	// Only authoritative disappearance of the bound sandbox is a terminal fact.
	lifecycle.endpointStatus = http.StatusNotFound
	require.Error(t, pass())
	require.False(t, current(t, steps, record.ID).Outcome.Lost, "missing endpoint must not imply missing environment")
	lifecycle.endpointStatus = 0
	lifecycle.inspectStatus = http.StatusServiceUnavailable
	require.Error(t, pass())
	require.False(t, current(t, steps, record.ID).Outcome.Lost, "temporary provider errors retain observation")
	lifecycle.inspectStatus = 0
	lifecycle.info = nil
	require.NoError(t, pass())
	lost := current(t, steps, record.ID)
	require.True(t, lost.Outcome.Lost)
	require.True(t, reported(lost, domain.ExecutionEnvironmentLost{WorkID: input.WorkID, StartAdmittedAt: lost.StartAdmittedAt}), "the loss is reported to the Work")
	require.False(t, lost.Retired(), "the loss stays recoverable until its Work retires the execution")
	require.NoError(t, steps.retire(ctx, input.WorkID, domain.RetiredByLoss, time.Now()))
	require.NoError(t, pass())
	after, _, err = latestAllocation(ctx, provisioning.db, input.WorkID)
	require.NoError(t, err)
	require.NotZero(t, after.DeletedAt)
	require.NotZero(t, after.ReleasedAt, "nothing is left to delete")
	require.Equal(t, 1, reserved)
	requests := len(receiver.requests)
	require.NoError(t, steps.shortSteps(ctx, record.ID), "a delayed reconnect of a retired execution decides nothing")
	require.Len(t, receiver.requests, requests, "and connects nothing")
}

// attachNativeCredentials gives native handoff tests the healthy credential
// management endpoint production preparation requires.
func attachNativeCredentials(t *testing.T, db *xorm.Engine, p *Provisioning, lifecycle *lifecycleFixture) {
	t.Helper()
	credentials, err := NewSandboxCredentials(db, p.sandboxes.client, p.sandboxes.config.Models, fixtureRepositoryCredentials{}, authorizationcatalogtest.NoServices{}, p.credentials.http, "encrypted-network")
	require.NoError(t, err)
	p.credentials = credentials
	policy, err := p.sandboxes.networkPolicy(context.Background())
	require.NoError(t, err)
	lifecycle.vault.policy = policy
}

// current is the execution as stored now.
func current(t *testing.T, steps *executionSteps, id string) *domain.AgentExecution {
	t.Helper()
	run, err := steps.executions.Get(context.Background(), id)
	require.NoError(t, err)
	return run
}

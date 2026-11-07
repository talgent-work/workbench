//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"golang.org/x/net/websocket"
	"xorm.io/xorm"
)

// machineRelay is Computer Gateway's relay to one Talgent Computer, serving
// the environment PUT and the execd subset for the Works on it.
type machineRelay struct {
	mu sync.Mutex
	// unreachable answers as the Gateway does without the machine's
	// connection.
	unreachable bool
	computerID  string
	token       string
	environment map[string]string
	modelProxy  bool
	// telemetry is the environment PUT's telemetry.
	telemetry map[string]string
	// credentials is the body of the last credentials PUT, nil once
	// forgotten.
	credentials           []byte
	deliveries, forgotten int
	// manifests are the bodies of the inputs PUTs, in order, each noted
	// with whether credentials had been delivered before it.
	manifests     [][]byte
	afterDelivery []bool
	files         map[string]string
	created       []struct{ Cwd, Command string }
	deleted       []string
	calls         []string
	// reconciliation serves the machine's reconciliation; nil answers 404,
	// as a Talgent Computer from before reconciliation does. Its rounds'
	// state is what the ping reports; without it the round is decided,
	// unless undecided says otherwise.
	reconciliation http.Handler
	undecided      bool
}

// reconciled is the state of the machine's current round, as its ping
// reports it.
func (m *machineRelay) reconciled() bool {
	m.mu.Lock()
	rounds, undecided := m.reconciliation, m.undecided
	m.mu.Unlock()
	if decider, ok := rounds.(interface{ decided() bool }); ok {
		return decider.decided()
	}
	return !undecided
}

func (m *machineRelay) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.calls = append(m.calls, r.Method+" "+r.URL.Path)
	unreachable := m.unreachable
	m.mu.Unlock()
	if unreachable {
		http.Error(w, "computer not connected", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/relay/"+m.computerID+"/reconciliation" && m.reconciliation != nil {
		m.reconciliation.ServeHTTP(w, r)
		return
	}
	prefix := "/relay/" + m.computerID + "/works/"
	rest, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_, path, _ := strings.Cut(rest, "/")
	path = "/" + path
	if r.Method == http.MethodPut && path == "/" {
		if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			AccessToken string            `json:"access_token"`
			Environment map[string]string `json:"environment"`
			ModelProxy  bool              `json:"model_proxy"`
			Telemetry   map[string]string `json:"telemetry"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		m.mu.Lock()
		m.token, m.environment, m.modelProxy, m.telemetry = body.AccessToken, body.Environment, body.ModelProxy, body.Telemetry
		m.mu.Unlock()
		_, _ = w.Write([]byte(`{"work_root":"/home/me/.talgent/works/w"}`))
		return
	}
	if (r.Method == http.MethodPut || r.Method == http.MethodDelete) && path == "/credentials" {
		if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		if r.Method == http.MethodPut {
			m.credentials = body
			m.deliveries++
		} else {
			m.credentials = nil
			m.forgotten++
		}
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method == http.MethodPut && path == "/inputs" {
		if r.Header.Get("X-Talgent-Control-Token") != "computer-control" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.manifests, m.afterDelivery = append(m.manifests, body), append(m.afterDelivery, m.credentials != nil)
		m.mu.Unlock()
		w.WriteHeader(http.StatusAccepted)
		return
	}
	m.mu.Lock()
	token := m.token
	m.mu.Unlock()
	if token == "" || r.Header.Get("X-EXECD-ACCESS-TOKEN") != token {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/ping":
		_ = json.NewEncoder(w).Encode(map[string]bool{"reconciled": m.reconciled()})
	case r.Method == http.MethodGet && path == "/files":
		m.mu.Lock()
		entries := []map[string]any{}
		for name, content := range m.files {
			entries = append(entries, map[string]any{"name": name, "kind": "file", "size": len(content), "modified_at": "2026-11-02T10:00:00Z"})
		}
		m.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	case r.Method == http.MethodGet && path == "/files/stat":
		m.mu.Lock()
		name := r.URL.Query().Get("path")
		content, ok := m.files[name]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": name, "kind": "file", "size": len(content), "modified_at": "2026-11-02T10:00:00Z"})
	case r.Method == http.MethodGet && path == "/files/content":
		m.mu.Lock()
		content, ok := m.files[r.URL.Query().Get("path")]
		m.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, content)
	case r.Method == http.MethodPost && path == "/pty":
		var body struct{ Cwd, Command string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		m.created = append(m.created, body)
		id := "harness-handle"
		if strings.Contains(body.Command, "node -e") {
			id = "preparation-handle"
		}
		m.mu.Unlock()
		_, _ = w.Write([]byte(`{"session_id":"` + id + `"}`))
	case r.Method == http.MethodGet && path == "/pty/preparation-handle/ws":
		websocket.Handler(func(ws *websocket.Conn) {
			defer ws.Close()
			_ = websocket.Message.Send(ws, `{"type":"connected","session_id":"preparation-handle","mode":"pipe","role":"holder"}`)
			_ = websocket.Message.Send(ws, append([]byte{1}, []byte(`{"results":[],"repositories":null}`+"\n")...))
			_ = websocket.Message.Send(ws, `{"type":"exit","exit_code":0}`)
		}).ServeHTTP(w, r)
	case r.Method == http.MethodDelete && strings.HasPrefix(path, "/pty/"):
		m.mu.Lock()
		m.deleted = append(m.deleted, strings.TrimPrefix(path, "/pty/"))
		m.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (m *machineRelay) set(change func(*machineRelay)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(m)
}

// managedWork is the execution port and recovery of Works on one Managed
// Computer, reached through a relay fixture, beside a Cloud.
type managedWork struct {
	db           *xorm.Engine
	relay        *machineRelay
	relayURL     string
	place        *managedPlace
	provisioning *Provisioning
	observations *ProcessObservations
	removals     *Removals
	port         *Environment
	mounts       *countingMounts
	feedback     *reclamationFeedback
	receiver     *nativeConnectionReceiver
	connections  *NativeConnections
	recovery     *EnvironmentRecovery
	inputs       *inputFiles
}

func newManagedWork(t *testing.T) *managedWork {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	f := &managedWork{db: db, relay: &machineRelay{computerID: managedComputerID}, place: &managedPlace{}, mounts: &countingMounts{}, feedback: &reclamationFeedback{}, inputs: &inputFiles{}}
	relayServer := httptest.NewServer(f.relay)
	t.Cleanup(relayServer.Close)
	f.relayURL = relayServer.URL
	machines, err := NewManagedComputers(relayServer.Client(), relayServer.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, API: managedAPIAddress, Telemetry: managedTelemetryAddress}, "computer-control", f.place)
	require.NoError(t, err)
	f.provisioning = NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, initial, nil)
	attachNativeCredentials(t, db, f.provisioning, lifecycle)
	// The Managed realization reads the Work's state as the Cloud's does.
	f.provisioning.managed, err = NewManagedEnvironment(db, machines, fixtureModels(), f.provisioning.credentials.heldWorks(), f.inputs, managedInputsRefresh, nil)
	require.NoError(t, err)

	f.receiver = &nativeConnectionReceiver{provisioning: f.provisioning}
	mux := http.NewServeMux()
	mux.Handle(dispatcherprivatev1connect.NewExecutionServiceHandler(f.receiver))
	dispatcher := httptest.NewServer(mux)
	t.Cleanup(dispatcher.Close)
	control := NewDispatcherControl(dispatcher.Client(), dispatcher.URL, "dispatcher-secret")
	f.observations, err = NewProcessObservations(f.provisioning, control)
	require.NoError(t, err)
	f.removals = NewRemovals(db, time.Now)
	resources, err := NewEnvironments(f.provisioning, f.removals, f.observations)
	require.NoError(t, err)
	f.port, err = NewEnvironment(resources, control, f.mounts, f.place)
	require.NoError(t, err)
	f.connections, err = NewNativeConnections(f.provisioning, f.provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret", f.feedback, command.NewRecordRepositoryCheckoutsHandler(initial))
	require.NoError(t, err)
	reclaimer, err := NewReclaimer(f.feedback)
	require.NoError(t, err)
	f.recovery, err = NewEnvironmentRecovery(f.provisioning, f.connections, reclaimer, f.removals, unread{})
	require.NoError(t, err)
	return f
}

// A Managed Work starts through the same execution port and recovery as a
// Cloud Work: its environment is opened on the machine through the relay, it
// prepares and launches in /workspace there, and Dispatcher is handed the
// relay as its execd endpoint. Its compute is released through the relay and
// its directory stays.
func TestManagedWorkStartsPreparesLaunchesAndConnectsThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	db, relay, provisioning, connections, port, recovery, observations, feedback, receiver, removals := f.db, f.relay, f.provisioning, f.connections, f.port, f.recovery, f.observations, f.feedback, f.receiver, f.removals
	mounts := f.mounts

	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	require.NoError(t, port.StartExecution(ctx, input))
	require.Zero(t, mounts.calls, "a Work on a Managed Computer acquires no mounts")
	record, found, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, realizationManaged, record.Realization, "the realization is recorded, never inferred")
	require.NotContains(t, string(record.SpecificationJSON), "HOME")
	require.Equal(t, "[]", string(record.AuthorizationIDs), "no Provider credential is redelivered to a Managed Work")

	require.NoError(t, submitPending(ctx, provisioning))
	require.NoError(t, connectLive(ctx, connections))
	record, _, err = latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.True(t, record.SandboxReady)
	require.True(t, record.EnvironmentPrepared)
	require.Equal(t, "harness-handle", record.ProcessID)
	require.Equal(t, input.WorkID, relay.environment["TALGENT_WORK_ID"])
	require.Equal(t, "1", relay.environment["DISABLE_AUTOUPDATER"])
	require.Equal(t, "1", relay.environment["CLAUDE_CODE_ENABLE_TELEMETRY"], "a Managed run reports telemetry as a Cloud run does")
	require.Equal(t, managedTelemetryAddress, relay.telemetry["endpoint"])
	require.False(t, relay.modelProxy, "the machine's own Claude login needs no model proxy")
	var spec managedSpecification
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	require.JSONEq(t, `{"gateway":{"url":"`+managedGatewayAddress+`","work_id":"`+input.WorkID+`","work_credential":"`+spec.WorkCredential+`"},"api":{"url":"`+managedAPIAddress+`"}}`, string(relay.credentials),
		"the machine's own Claude login gets the platform's credentials only: the Gateway's, and where the native API answers")
	require.Zero(t, record.CredentialsExpireAt)
	require.Empty(t, record.ModelCredentialDigest)
	require.Len(t, relay.created, 2)
	for _, process := range relay.created {
		require.Equal(t, "/workspace", process.Cwd, "the machine maps /workspace to the Work root")
	}
	require.Equal(t, record.LaunchCommand, relay.created[1].Command)
	require.Len(t, receiver.requests, 1)
	connected := receiver.requests[0]
	require.Equal(t, f.relayURL+"/relay/"+managedComputerID+"/works/"+input.WorkID, connected.GetEndpoint(), "Dispatcher reaches the machine through the relay")
	require.Equal(t, relay.token, connected.GetAccessToken())
	require.Equal(t, "harness-handle", connected.GetProcessId())
	require.Equal(t, record.NativeSessionID, connected.GetNativeSessionId())
	for _, call := range relay.calls {
		require.True(t, strings.HasPrefix(call, http.MethodPut+" /relay/") || strings.Contains(call, "/relay/"+managedComputerID+"/works/"+input.WorkID) || call == http.MethodGet+" /relay/"+managedComputerID+"/reconciliation", call)
	}

	credentials, err := NewWorkCredentials(provisioning)
	require.NoError(t, err)
	require.NoError(t, credentials.VerifyWorkCredential(ctx, input.WorkID, spec.WorkCredential))
	require.Error(t, credentials.VerifyWorkCredential(ctx, input.WorkID, "forged"))

	// The process exited: its compute is reclaimed through the relay, and
	// the Work's directory stays.
	exited, err := observations.AcknowledgeExit(ctx, input.WorkID, "harness-handle")
	require.NoError(t, err)
	require.True(t, exited)
	// Agent applies the confirmation by observing the process, which a
	// Managed allocation reports released only from its completed removal;
	// Dispatcher no longer holds a connection of the exited process.
	var confirmed command.WorkProcessObservation
	feedback.observe = func(ctx context.Context, request command.ExecutionFeedback) error {
		var err error
		confirmed, err = observations.ObserveProcess(ctx, request.WorkID)
		return err
	}
	require.NoError(t, port.ReclaimCompute(ctx, input.WorkID))
	_, err = recovery.ReconcileAllocation(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, confirmed.ComputeReclaimed, "the confirmation observes the release it confirms")
	require.ElementsMatch(t, []string{"preparation-handle", "harness-handle"}, relay.deleted)
	require.Len(t, feedback.requests, 1)
	require.Equal(t, command.ComputeReclaimed, feedback.requests[0].Kind)
	observation, err := observations.ObserveProcess(ctx, input.WorkID)
	require.NoError(t, err)
	require.True(t, observation.ComputeReclaimed)
	pending, err := pendingRemovals(ctx, removals)
	require.NoError(t, err)
	require.Empty(t, pending)
	for _, call := range relay.calls {
		require.NotEqual(t, http.MethodDelete+" /relay/"+managedComputerID+"/works/"+input.WorkID, call, "the Work's directory is never deleted")
	}

	// Resume runs on the same Computer and continues the native session.
	input.ContextMode = domain.ContextContinue
	input.HarnessSessionReference = "context"
	require.NoError(t, port.StartExecution(ctx, input))
	resumed, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.NotEqual(t, record.ID, resumed.ID)
	require.Contains(t, resumed.LaunchCommand, "'--resume' '"+record.NativeSessionID+"'")
	require.Equal(t, realizationManaged, resumed.Realization)
	resumedSpec, err := decodeManagedSpecification(resumed)
	require.NoError(t, err)
	require.Equal(t, managedComputerID, resumedSpec.ComputerID)
}

// A Work of a Project on a Managed Computer has no read-only mount, even
// one its creating caller supplied: its environment names its Project and no
// TALGENT_KNOWLEDGE_ROOT, and it reaches its Project's tools through the
// Talgent Connector, declared at the machine's local proxy.
func TestAManagedWorkOfAProjectReadsReferencesThroughTheConnector(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode, input.ProjectID = domain.ContextFresh, "project"
	input.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "reference", Reference: "project"}}
	require.NoError(t, f.port.StartExecution(ctx, input))
	require.Zero(t, f.mounts.calls, "no read-only mount is acquired")
	require.NoError(t, submitPending(ctx, f.provisioning))
	require.NoError(t, connectLive(ctx, f.connections))
	require.Equal(t, "project", f.relay.environment["TALGENT_PROJECT_ID"])
	require.NotContains(t, f.relay.environment, "TALGENT_KNOWLEDGE_ROOT")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Contains(t, record.LaunchCommand, `"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}`)
}

// countingMounts counts mount acquisitions; a Managed start makes none.
type countingMounts struct{ calls int }

func (m *countingMounts) AcquireMounts(context.Context, domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	m.calls++
	return domain.SandboxMounts{}, nil
}

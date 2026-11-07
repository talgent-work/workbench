//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
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

// managedWork reconciles the executions of Works on one Managed Computer,
// reached through a relay fixture, beside a Cloud.
type managedWork struct {
	db           *xorm.Engine
	relay        *machineRelay
	relayURL     string
	place        *managedPlace
	provisioning *Provisioning
	mounts       *countingMounts
	receiver     *nativeConnectionReceiver
	connections  *NativeConnections
	steps        *executionSteps
	owners       *readingProcess
	inputs       *inputFiles
	// works are the Works as Agent records them for the machine's
	// reconciliation.
	works *workSource
}

// workSource holds the Works a case records, swapped by the case.
type workSource struct {
	mu   sync.Mutex
	repo domain.AgentWorkRepository
}

func (w *workSource) set(repo domain.AgentWorkRepository) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.repo = repo
}

func (w *workSource) current() domain.AgentWorkRepository {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.repo
}

func (w *workSource) Get(ctx context.Context, id string) (*domain.AgentWork, error) {
	return w.current().Get(ctx, id)
}

func (w *workSource) FindBySubject(ctx context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	return w.current().FindBySubject(ctx, agentID, subject)
}

func (w *workSource) Save(ctx context.Context, work *domain.AgentWork) error {
	return w.current().Save(ctx, work)
}

func newManagedWork(t *testing.T) *managedWork {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	provider, _, lifecycle := sandboxClient(t)
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	f := &managedWork{db: db, relay: &machineRelay{computerID: managedComputerID}, place: &managedPlace{}, mounts: &countingMounts{}, inputs: &inputFiles{}, owners: &readingProcess{},
		works: &workSource{repo: infrastructure.NewAgentWorkRepository(db, time.Now)}}
	relayServer := httptest.NewServer(f.relay)
	t.Cleanup(relayServer.Close)
	f.relayURL = relayServer.URL
	machines, err := NewManagedComputers(relayServer.Client(), relayServer.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, API: managedAPIAddress, Telemetry: managedTelemetryAddress}, "computer-control", f.place)
	require.NoError(t, err)
	f.provisioning = NewProvisioning(db, provider, fixtureSandboxCredentials(db, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, initial, nil)
	attachNativeCredentials(t, db, f.provisioning, lifecycle)
	f.provisioning.managed, err = NewManagedEnvironment(db, machines, fixtureModels(), f.works, f.inputs, managedInputsRefresh)
	require.NoError(t, err)

	f.receiver = &nativeConnectionReceiver{provisioning: f.provisioning}
	mux := http.NewServeMux()
	mux.Handle("/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, f.receiver) }))
	dispatcher := httptest.NewServer(mux)
	t.Cleanup(dispatcher.Close)
	f.connections, err = NewNativeConnections(f.provisioning, f.provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret", command.NewRecordRepositoryCheckoutsHandler(initial))
	require.NoError(t, err)
	f.steps = newExecutionStepsWith(t, f.provisioning, f.connections, f.owners, f.provisioning.managed, time.Now, f.mounts)
	return f
}

// begin begins the execution of the start input describes, realized where
// its Computer is, as the Policies do from the Work's admitted start.
func (f *managedWork) begin(t *testing.T, input domain.LaunchSpecification) allocationRecord {
	t.Helper()
	ctx := context.Background()
	place, err := f.place.DescribeComputer(ctx, input.ComputerID)
	require.NoError(t, err)
	input.ComputerName = place.Name
	realization := domain.RealizationCloud
	if place.Managed {
		realization = domain.RealizationManaged
	}
	run, err := domain.BeginExecution(domain.ExecutionBeginning{
		Admitted: domain.WorkStartAdmitted{WorkID: input.WorkID, StartAdmittedAt: time.Now(), DeskID: "desk-" + input.WorkID},
		Launch:   input, Realization: realization,
	})
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(ctx, run))
	record, _, err := f.provisioning.get(ctx, run.ID)
	require.NoError(t, err)
	return record
}

// launch reconciles the execution until its Harness process is launched and
// handed to Dispatcher: its creation, first delivery, input sync,
// preparation and start.
func (f *managedWork) launch(t *testing.T, id string) allocationRecord {
	t.Helper()
	ctx := context.Background()
	var failures []error
	for range 4 {
		failures = append(failures, f.steps.reconcile(ctx, id))
		record, _, err := f.provisioning.get(ctx, id)
		require.NoError(t, err)
		if record.ProcessID != "" {
			return record
		}
	}
	require.NoError(t, errors.Join(failures...))
	require.Fail(t, "the execution launched no Harness process")
	return allocationRecord{}
}

// decided is what the execution decides from its observation now, without
// taking it.
func (f *managedWork) decided(t *testing.T, id string) []domain.ExecutionStepKind {
	t.Helper()
	run := current(t, f.steps, id)
	observation, err := f.steps.environment.Observe(context.Background(), run)
	require.NoError(t, err)
	decision, err := run.Reconcile(observation, time.Now())
	require.NoError(t, err)
	kinds := []domain.ExecutionStepKind{}
	for _, step := range decision.Steps {
		kinds = append(kinds, step.Kind)
	}
	return kinds
}

// markInputsChanged marks the inputs of every execution reading owner's File
// Space due, as Storage's change report does.
func (f *managedWork) markInputsChanged(t *testing.T, owner string) []string {
	t.Helper()
	ctx := context.Background()
	ids, err := infrastructure.NewExecutionQueryRepository(f.db).ListExecutionsReadingInputs(ctx, owner)
	require.NoError(t, err)
	marked, err := f.steps.handler.MarkInputsChanged(ctx, ids)
	require.NoError(t, err)
	return marked
}

// startManaged begins and launches a Managed Work's run and answers its
// execution's record.
func startManaged(t *testing.T, f *managedWork, workID string) allocationRecord {
	t.Helper()
	input := managedRequest()
	input.WorkID = workID
	input.ContextMode = domain.ContextFresh
	record := f.launch(t, f.begin(t, input).ID)
	require.False(t, record.ProcessExited)
	return record
}

// exit records the launched process's exit and retires the execution as its
// Work asks.
func (f *managedWork) exit(t *testing.T, record allocationRecord) {
	t.Helper()
	ctx := context.Background()
	run, err := f.steps.executions.Get(ctx, record.ID)
	require.NoError(t, err)
	run.RecordExit(record.ProcessID, domain.HarnessExit{})
	require.NoError(t, f.steps.executions.Save(ctx, run))
	require.NoError(t, f.steps.retire(ctx, record.SessionID, domain.RetiredByExit, time.Now()))
}

// A Managed Work's execution is reconciled as a Cloud Work's is: its
// environment is opened on the machine through the relay, it prepares and
// launches in /workspace there, and Dispatcher is handed the relay as its
// execd endpoint. Its compute is released through the relay and its
// directory stays.
func TestManagedWorkStartsPreparesLaunchesAndConnectsThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	db, relay, provisioning, receiver := f.db, f.relay, f.provisioning, f.receiver

	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	begun := f.begin(t, input)
	require.Equal(t, realizationManaged, begun.Realization, "the realization is recorded, never inferred")
	require.Equal(t, "[]", string(begun.AuthorizationIDs), "no Provider credential is redelivered to a Managed Work")

	record := f.launch(t, begun.ID)
	require.Zero(t, f.mounts.calls, "a Work on a Managed Computer acquires no mounts")
	require.NotContains(t, string(record.SpecificationJSON), "HOME")
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

	// The process exited and its Work retired the execution: its compute is
	// released through the relay, and the Work's directory stays.
	f.exit(t, record)
	require.NoError(t, f.steps.reconcile(ctx, record.ID))
	require.ElementsMatch(t, []string{"preparation-handle", "harness-handle"}, relay.deleted)
	released := current(t, f.steps, record.ID)
	require.Equal(t, domain.ExecutionReleased, released.State, "the release is recorded once its processes are deleted")
	require.True(t, reported(released, domain.ExecutionReleasedEvent{WorkID: input.WorkID, StartAdmittedAt: released.StartAdmittedAt}))
	pending, err := unreleased(ctx, db)
	require.NoError(t, err)
	require.Empty(t, pending)
	for _, call := range relay.calls {
		require.NotEqual(t, http.MethodDelete+" /relay/"+managedComputerID+"/works/"+input.WorkID, call, "the Work's directory is never deleted")
	}

	// Resume runs on the same Computer and continues the native session.
	input.ContextMode = domain.ContextContinue
	input.HarnessSessionReference = "context"
	next := f.begin(t, input)
	require.NotEqual(t, record.ID, next.ID)
	require.NoError(t, f.steps.reconcile(ctx, next.ID))
	resumed, _, err := latestAllocation(ctx, db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, next.ID, resumed.ID)
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
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode, input.ProjectID = domain.ContextFresh, "project"
	input.ReadOnlyMounts = []domain.ReadOnlyMount{{Name: "reference", Reference: "project"}}
	record := f.launch(t, f.begin(t, input).ID)
	require.Zero(t, f.mounts.calls, "no read-only mount is acquired")
	require.Equal(t, "project", f.relay.environment["TALGENT_PROJECT_ID"])
	require.NotContains(t, f.relay.environment, "TALGENT_KNOWLEDGE_ROOT")
	require.Contains(t, record.LaunchCommand, `"talgent":{"headers":{"Authorization":"Bearer ${TALGENT_PROXY_TOKEN}"},"type":"http","url":"${TALGENT_PROXY_URL}/mcp"}`)
}

// countingMounts counts mount acquisitions; a Managed start makes none.
type countingMounts struct{ calls int }

func (m *countingMounts) AcquireMounts(context.Context, domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	m.calls++
	return domain.SandboxMounts{}, nil
}

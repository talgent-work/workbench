//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	jimuxorm "github.com/go-jimu/contrib/transaction/xorm"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/go/dispatcher/private/v1/dispatcherprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
	"xorm.io/xorm"
)

// managedWork reconciles the executions of Works on one Managed Computer,
// reached through a relay fixture, beside a Cloud.
type managedWork struct {
	db           *xorm.Engine
	relay        *machineRelay
	relayURL     string
	place        *managedPlace
	provisioning *Provisioning
	storage      *managedStorage
	receiver     *nativeConnectionReceiver
	connections  *NativeConnections
	steps        *executionSteps
	owners       *readingProcess
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

func (w *workSource) FindByWorkKey(ctx context.Context, agentID string, subject domain.WorkKey) (*domain.AgentWork, error) {
	return w.current().FindByWorkKey(ctx, agentID, subject)
}

func (w *workSource) Save(ctx context.Context, work *domain.AgentWork) error {
	return w.current().Save(ctx, work)
}

func newManagedWork(t *testing.T) *managedWork {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	jdb, err := jimuxorm.New(db)
	require.NoError(t, err)
	provider, _, lifecycle := sandboxClient(t)
	f := &managedWork{db: db, relay: &machineRelay{computerID: managedComputerID}, place: &managedPlace{}, storage: &managedStorage{}, owners: &readingProcess{},
		works: &workSource{repo: infrastructure.NewAgentWorkRepository(jdb, time.Now)}}
	relayServer := httptest.NewServer(f.relay)
	t.Cleanup(relayServer.Close)
	f.relayURL = relayServer.URL
	machines, err := NewManagedComputers(relayServer.Client(), relayServer.URL, ManagedAddresses{Gateway: managedGatewayAddress, API: managedAPIAddress, Telemetry: managedTelemetryAddress}, "computer-control", f.place)
	require.NoError(t, err)
	f.provisioning = NewProvisioning(jdb, provider, fixtureSandboxCredentials(db, jdb, provider), NativeLaunchConfig{Binary: "claude"}, time.Now, nil)
	attachNativeCredentials(t, db, f.provisioning, lifecycle)
	f.provisioning.managed, err = NewManagedEnvironment(jdb, machines, fixtureModels(), f.works)
	require.NoError(t, err)

	f.receiver = &nativeConnectionReceiver{provisioning: f.provisioning}
	mux := http.NewServeMux()
	mux.Handle("/"+dispatcherprivatev1connect.ExecutionServiceName+"/", connecttest.Handler(func(s *connect.Server) { dispatcherprivatev1connect.RegisterExecutionServiceHandler(s, f.receiver) }))
	dispatcher := httptest.NewServer(mux)
	t.Cleanup(dispatcher.Close)
	f.connections, err = NewNativeConnections(f.provisioning, f.provisioning.credentials, dispatcher.Client(), dispatcher.URL, "dispatcher-secret")
	require.NoError(t, err)
	f.steps = newExecutionStepsWith(t, f.provisioning, f.connections, f.owners, f.provisioning.managed, time.Now, f.storage)
	return f
}

// begin begins the execution of the start input describes, realized where
// its Computer is, as the Policies do from the Work's admitted start.
func (f *managedWork) begin(t *testing.T, input domain.LaunchSpecification) executionRecord {
	t.Helper()
	return f.beginWith(t, input, nil)
}

// beginWith begins it with the current values of the Work's Personal
// Variables.
func (f *managedWork) beginWith(t *testing.T, input domain.LaunchSpecification, variables []domain.PersonalVariable) executionRecord {
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
		Launch:   input, Realization: realization, PersonalVariables: variables,
	})
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(ctx, run))
	record, _, err := f.provisioning.get(ctx, run.ID)
	require.NoError(t, err)
	return record
}

// launch reconciles the execution until its Harness process is launched and
// handed to Dispatcher: its creation, first delivery, preparation and
// start.
func (f *managedWork) launch(t *testing.T, id string) executionRecord {
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
	return executionRecord{}
}

// reading is Dispatcher reading the record's launched process at the relay
// endpoint it was handed.
func (f *managedWork) reading(t *testing.T, record executionRecord) StreamReading {
	t.Helper()
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	return StreamReading{ProcessID: record.ProcessID, Endpoint: f.provisioning.managed.machines.endpoint(spec, record.SessionID)}
}

// startManaged begins and launches a Managed Work's run and answers its
// execution's record.
func startManaged(t *testing.T, f *managedWork, workID string) executionRecord {
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
func (f *managedWork) exit(t *testing.T, record executionRecord) {
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
// directory stays. Neither its start nor its Resume hands the machine its
// located inputs: the Work's Agent downloads those it needs (Managed input
// reading).
func TestManagedWorkStartsPreparesLaunchesAndConnectsThroughTheRelay(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	db, relay, receiver := f.db, f.relay, f.receiver

	input := readingInputs(managedRequest(), caseInputs())
	input.ContextMode = domain.ContextFresh
	begun := f.begin(t, input)
	require.Equal(t, realizationManaged, begun.Realization, "the realization is recorded, never inferred")
	require.Equal(t, "[]", string(begun.AuthorizationIDs), "no Provider credential is redelivered to a Managed Work")

	record := f.launch(t, begun.ID)
	require.Zero(t, f.storage.mounts, "a Work on a Managed Computer acquires no mounts")
	require.NotContains(t, string(record.SpecificationJSON), "HOME")
	require.True(t, record.CredentialsDelivered)
	require.True(t, record.PreparationEnded)
	require.Equal(t, "harness-handle", record.ProcessID)
	require.Equal(t, input.WorkID, relay.environment["TALGENT_WORK_ID"])
	require.Equal(t, "1", relay.environment["DISABLE_AUTOUPDATER"])
	require.Equal(t, "1", relay.environment["CLAUDE_CODE_ENABLE_TELEMETRY"], "a Managed run reports telemetry as a Cloud run does")
	require.Equal(t, managedTelemetryAddress, relay.telemetry["endpoint"])
	require.False(t, relay.modelProxy, "the machine's own Claude login needs no model proxy")
	var spec managedSpecification
	require.NoError(t, json.Unmarshal(record.SpecificationJSON, &spec))
	require.JSONEq(t, `{"gateway":{"url":"`+managedGatewayAddress+`","identity_token":"agent-token-`+input.WorkID+`","space_token":"space-token"},"api":{"url":"`+managedAPIAddress+`"}}`, string(relay.credentials),
		"the machine's own Claude login gets the platform's credentials only: the Identity Token with its publication target's Space Token, and where the entry answers")
	requireTokenRenewal(t, record)
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
	requireNoInputsCall(t, relay)

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
	resumed := f.launch(t, next.ID)
	require.Equal(t, next.ID, resumed.ID)
	require.Contains(t, resumed.LaunchCommand, "--resume '"+record.NativeSessionID+"'")
	require.Equal(t, realizationManaged, resumed.Realization)
	resumedSpec, err := decodeManagedSpecification(resumed)
	require.NoError(t, err)
	require.Equal(t, managedComputerID, resumedSpec.ComputerID)
	requireNoInputsCall(t, relay)
}

// requireNoInputsCall asserts that no call reached a Work's inputs on the
// machine.
func requireNoInputsCall(t *testing.T, relay *machineRelay) {
	t.Helper()
	relay.mu.Lock()
	defer relay.mu.Unlock()
	for _, call := range relay.calls {
		require.False(t, strings.HasSuffix(call, "/inputs"), "the platform hands the machine no inputs: %s", call)
	}
}

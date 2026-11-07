//go:build integration

package execution

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"
)

// Only the external compute/credential-management provider is controlled;
// Agent persistence, provisioning, credential delivery and the reconciliation
// of each AgentExecution are real. No case using it reaches Dispatcher, so
// Dispatcher is a server that fails the case on any call instead of a running
// replica holding the shared Redis.
type nativeWorkLifecycleFixture struct {
	db           *xorm.Engine
	works        *infrastructure.AgentWorkRepository
	definitions  *infrastructure.AgentDefinitionRepository
	provisioning *Provisioning
	connections  *NativeConnections
	executions   *AgentExecutionRepository
	reconcile    *command.ReconcileExecutionHandler
	coordination *command.ExecutionCoordination
	external     *lifecycleFixture
	// readings are the processes Dispatcher reads, by Work.
	readings *streamReadings
}

// streamReadings is Dispatcher's stream ownership, set by the case.
type streamReadings struct {
	mu   sync.Mutex
	read map[string]StreamReading
}

func (s *streamReadings) set(workID string, reading StreamReading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.read == nil {
		s.read = map[string]StreamReading{}
	}
	s.read[workID] = reading
}

func (s *streamReadings) Reading(_ context.Context, workID string) (StreamReading, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	reading, found := s.read[workID]
	return reading, found, nil
}

func newNativeWorkLifecycle(t *testing.T, ctx context.Context) *nativeWorkLifecycleFixture {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	dispatchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected Dispatcher call %s", r.URL.Path)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(dispatchServer.Close)
	provider, _, external := sandboxClient(t)
	credentials, err := NewSandboxCredentials(db, provider.client, provider.config.Models, fixtureRepositoryCredentials{}, authorizationcatalogtest.NoServices{}, http.DefaultClient, "encrypted-network")
	require.NoError(t, err)
	policy, err := provider.networkPolicy(ctx)
	require.NoError(t, err)
	external.vault.policy = policy
	initial := infrastructure.NewInitialExecutionRequests(db, time.Now)
	provisioning := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, initial, nil)
	connections, err := NewNativeConnections(provisioning, credentials, dispatchServer.Client(), dispatchServer.URL, "lifecycle-control", command.NewRecordRepositoryCheckoutsHandler(initial))
	require.NoError(t, err)
	readings := &streamReadings{}
	environment, err := NewExecutionEnvironment(provisioning, connections, readings, nil)
	require.NoError(t, err)
	executions := NewAgentExecutionRepository(db, time.Now)
	harness := commandtest.NewHarness()
	computers := &cloudDesks{}
	return &nativeWorkLifecycleFixture{
		db: db, works: works, definitions: definitions, provisioning: provisioning, connections: connections, external: external, executions: executions, readings: readings,
		reconcile:    command.NewReconcileExecutionHandler(time.Now, executions, environment, harness, computers, directoryMounts{root: t.TempDir()}, initial),
		coordination: command.NewExecutionCoordination(time.Now, works, executions, computers, harness),
	}
}

// fixtureCloudID is the Cloud every fixture Work is admitted on.
const fixtureCloudID = "01a19bc3-075a-7c57-b42b-7fdf73b611ed"

// cloudDesks describes every Computer as a Cloud whose Desks are seated at
// once, and records the Desks that left.
type cloudDesks struct {
	cloudComputers
	mu   sync.Mutex
	left []string
}

func (*cloudDesks) DeskSeated(context.Context, string) (time.Time, bool, error) {
	return time.Time{}, true, nil
}

func (c *cloudDesks) LeaveDesk(_ context.Context, deskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.left = append(c.left, deskID)
	return nil
}

func (c *cloudDesks) departures() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.left...)
}

// createWithAuthorization registers a Work admitted on the Cloud with the
// authorization it uses, begins its start's execution as the Policies would,
// and submits its environment's creation as the execution's reconciliation
// does.
func (f *nativeWorkLifecycleFixture) createWithAuthorization(t *testing.T, ctx context.Context, snapshot domain.CapabilitySnapshot, authorization domain.WorkAuthorizationInput) (*domain.AgentWork, string) {
	t.Helper()
	snapshot.Connectors = request().Capabilities.Connectors
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Native lifecycle", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "lifecycle-owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, f.definitions.Save(ctx, definition))
	definition, err = f.definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	work, err := domain.NewAgentWork(domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "lifecycle-owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: uuid.NewString(), HomeBinding: definition.HomeBinding, Capabilities: snapshot, AuthorizationInput: authorization}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: fixtureCloudID, DeskID: "desk-" + uuid.NewString()}, ModelConfigurationProvided: true})
	require.NoError(t, err)
	startID := uuid.NewString()
	require.NoError(t, f.works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "lifecycle-owner", DefinitionVersion: definition.Version, Request: agentquery.StartRequest{ID: startID, Kind: agentquery.StartKindStart, WorkID: work.ID}}))
	require.NoError(t, f.coordination.BeginExecution(ctx, work.ID))
	run, err := f.executions.Latest(ctx, work.ID)
	require.NoError(t, err)
	_, err = f.reconcile.Reconcile(ctx, run.ID, false)
	require.NoError(t, err)
	return work, startID
}

// run launches the Work's bound execution as the process processID that
// Dispatcher reads, its credentials delivered.
func (f *nativeWorkLifecycleFixture) run(t *testing.T, ctx context.Context, workID, processID string) *domain.AgentExecution {
	t.Helper()
	run, err := f.executions.Latest(ctx, workID)
	require.NoError(t, err)
	require.NotEmpty(t, run.Environment, "the execution bound its created environment")
	run.RecordCredentialDelivery(domain.CredentialDelivery{ExpiresAt: time.Now().Add(time.Hour)})
	run.RecordPreparation()
	_, err = run.RecordLaunch(processID)
	require.NoError(t, err)
	require.NoError(t, f.executions.Save(ctx, run))
	f.readings.set(workID, StreamReading{ProcessID: processID})
	run, err = f.executions.Get(ctx, run.ID)
	require.NoError(t, err)
	return run
}

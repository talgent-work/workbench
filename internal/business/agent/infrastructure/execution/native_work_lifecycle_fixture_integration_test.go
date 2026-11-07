//go:build integration

package execution

import (
	"context"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"xorm.io/xorm"
)

// Only the external compute/credential-management provider is controlled;
// Agent persistence, provisioning and credential delivery are real. No case
// using it reaches Dispatcher, so Dispatcher is a server that fails the case on
// any call instead of a running replica holding the shared Redis.
type nativeWorkLifecycleFixture struct {
	db           *xorm.Engine
	works        *infrastructure.AgentWorkRepository
	definitions  *infrastructure.AgentDefinitionRepository
	provisioning *Provisioning
	connections  *NativeConnections
	reclaimer    *Reclaimer
	external     *lifecycleFixture
	requests     domain.ExecutionPort
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
	control := NewDispatcherControl(dispatchServer.Client(), dispatchServer.URL, "lifecycle-control")
	provider, _, external := sandboxClient(t)
	credentials, err := NewSandboxCredentials(db, provider.client, provider.config.Models, works, fixtureRepositoryCredentials{}, authorizationcatalogtest.NoServices{}, http.DefaultClient, "encrypted-network")
	require.NoError(t, err)
	policy, err := provider.networkPolicy(ctx)
	require.NoError(t, err)
	external.vault.policy = policy
	provisioning := NewProvisioning(db, provider, credentials, NativeLaunchConfig{Binary: "claude"}, time.Now, infrastructure.NewInitialExecutionRequests(db, time.Now), nil)
	observations, err := NewProcessObservations(provisioning, control)
	require.NoError(t, err)
	resources, err := NewEnvironments(provisioning, observations)
	require.NoError(t, err)
	environment, err := NewEnvironment(resources, control, directoryMounts{root: t.TempDir()}, cloudComputers{})
	require.NoError(t, err)
	requests := environment
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, works, observations, requests, control, &commandtest.InputSettlements{})
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	connections, err := NewNativeConnections(provisioning, credentials, dispatchServer.Client(), dispatchServer.URL, "lifecycle-control", feedback, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	return &nativeWorkLifecycleFixture{db: db, works: works, definitions: definitions, provisioning: provisioning, connections: connections, reclaimer: reclaimer, external: external, requests: requests}
}

func (f *nativeWorkLifecycleFixture) createWithAuthorization(t *testing.T, ctx context.Context, snapshot domain.CapabilitySnapshot, authorization domain.WorkAuthorizationInput) (*domain.AgentWork, string) {
	t.Helper()
	snapshot.Connectors = request().Capabilities.Connectors
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Native lifecycle", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "lifecycle-owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, f.definitions.Save(ctx, definition))
	definition, err = f.definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	requests := commandtest.Hold(f.requests)
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "lifecycle-owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: uuid.NewString(), HomeBinding: definition.HomeBinding, Capabilities: snapshot, AuthorizationInput: authorization}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, requests)
	require.NoError(t, err)
	startID := uuid.NewString()
	require.NoError(t, f.works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "lifecycle-owner", DefinitionVersion: definition.Version, Request: agentquery.StartRequest{ID: startID, Kind: agentquery.StartKindStart, WorkID: work.ID}}))
	require.NoError(t, requests.Send(ctx))
	require.NoError(t, submitPending(ctx, f.provisioning))
	return work, startID
}

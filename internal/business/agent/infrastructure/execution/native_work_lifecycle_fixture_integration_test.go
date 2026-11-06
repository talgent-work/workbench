//go:build integration

package execution

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/pkg/mysqltest"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	agentapp "github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	agenttransport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	dispatchapp "github.com/talgent-work/workbench/internal/business/dispatcher/application"
	dispatchcommand "github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	dispatcherredis "github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/redis"
	dispatchtransport "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"xorm.io/xorm"
)

// Only the external compute/credential-management provider and telemetry sink
// are controlled. Agent, Dispatcher, persistence and native execution are real.
type nativeWorkLifecycleFixture struct {
	db            *xorm.Engine
	works         *infrastructure.AgentWorkRepository
	definitions   *infrastructure.AgentDefinitionRepository
	provisioning  *Provisioning
	connections   *NativeConnections
	removals      *Removals
	reclaimer     *Reclaimer
	external      *lifecycleFixture
	requests      command.ExecutionRequestsFactory
	feedback      *command.ApplyExecutionFeedbackHandler
	recovery      *command.RecoverWorkHandler
	authorization *infrastructure.WorkAuthorizer
	control       *DispatcherControl
	storage       string
}

func newNativeWorkLifecycle(t *testing.T, ctx context.Context) *nativeWorkLifecycleFixture {
	t.Helper()
	db, err := xorm.NewEngine("mysql", mysqltest.Database(t))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	app := &agentapp.Application{Works: works, PluginResults: infrastructure.NewPluginResults(db, time.Now)}
	agentMux := http.NewServeMux()
	path, handler := agentprivatev1connect.NewExecutionFeedbackServiceHandler(agenttransport.NewFeedbackHandler(app, nil, "lifecycle-feedback"))
	agentMux.Handle(path, handler)
	agentServer := httptest.NewServer(agentMux)
	t.Cleanup(agentServer.Close)
	telemetry := &nativeLifecycleTelemetry{}
	rdb := goredis.NewClient(&goredis.Options{Addr: middlewaretest.Redis(t)})
	t.Cleanup(func() { _ = rdb.Close() })
	ownership := dispatcherredis.NewOwnership(rdb, "lifecycle", "127.0.0.1:0", 6*time.Second, time.Hour)
	buffer := dispatcherredis.NewInputBuffer(rdb)
	manager, err := dispatcher.NewNativeExecutions(http.DefaultClient, agentfeedback.NewClient(agentServer.Client(), agentServer.URL, "lifecycle-feedback"), newLifecycleDelivery(t, telemetry), ownership, buffer, dispatcherredis.OwnershipConfig{}.WithDefaults(), slog.Default())
	require.NoError(t, err)
	require.NoError(t, manager.Start(ctx))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, manager.Shutdown(cleanup))
	})
	dispatchApplication := dispatchapp.NewApplication(manager, dispatchcommand.NewConnectExecutionHandler(manager), dispatchcommand.NewEnqueueInputHandler(buffer), dispatchcommand.NewDropInputHandler(buffer), dispatchcommand.NewStopExecutionHandler(manager), manager, manager, manager, manager, buffer)
	_, dispatchHandler := dispatcherprivatev1connect.NewExecutionServiceHandler(dispatchtransport.NewExecutionHandler(dispatchApplication, "lifecycle-control"))
	dispatchServer := httptest.NewServer(dispatchHandler)
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
	storage := t.TempDir()
	removals := NewRemovals(db, time.Now)
	resources, err := NewEnvironments(provisioning, removals, observations)
	require.NoError(t, err)
	environment, err := NewEnvironment(resources, control, directoryMounts{root: storage}, cloudComputers{})
	require.NoError(t, err)
	requests := infrastructure.NewExecutionRequestsFactory(environment)
	feedback := command.NewApplyExecutionFeedbackHandler(time.Now, works, observations, requests, control, &commandtest.InputSettlements{})
	app.Commands.ApplyExecutionFeedback = feedback
	reclaimer, err := NewReclaimer(feedback)
	require.NoError(t, err)
	connections, err := NewNativeConnections(provisioning, credentials, dispatchServer.Client(), dispatchServer.URL, "lifecycle-control", feedback, command.NewRecordRepositoryCheckoutsHandler(infrastructure.NewInitialExecutionRequests(db, time.Now)))
	require.NoError(t, err)
	return &nativeWorkLifecycleFixture{db: db, works: works, definitions: definitions, provisioning: provisioning, connections: connections, removals: removals, reclaimer: reclaimer, external: external, requests: requests, feedback: feedback, recovery: command.NewRecoverWorkHandler(time.Now, works, requests, control, &commandtest.InputSettlements{}, nil, nil), authorization: infrastructure.NewWorkAuthorizer(db), control: control, storage: storage}
}

func (f *nativeWorkLifecycleFixture) createWithAuthorization(t *testing.T, ctx context.Context, snapshot domain.CapabilitySnapshot, authorization domain.WorkAuthorizationInput) (*domain.AgentWork, string) {
	t.Helper()
	snapshot.Connectors = request().Capabilities.Connectors
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"owner"}, Purpose: "work", AgentName: "Native lifecycle", UsageScope: domain.UsageScope{Kind: "employee", Reference: uuid.NewString()}, HomeBinding: domain.HomeBinding{OwnerID: "lifecycle-owner", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, f.definitions.Save(ctx, definition))
	definition, err = f.definitions.Get(ctx, definition.ID)
	require.NoError(t, err)
	requests := f.requests()
	work, err := domain.NewAgentWork(ctx, domain.WorkInput{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", WorkModel: domain.WorkModelSelection{ConfigurationID: "default"}, StartedBy: "lifecycle-owner", ID: uuid.NewString(), AgentID: definition.ID, WorkspaceReference: uuid.NewString(), HomeBinding: definition.HomeBinding, Capabilities: snapshot, AuthorizationInput: authorization}, domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true}, requests)
	require.NoError(t, err)
	startID := uuid.NewString()
	require.NoError(t, f.works.RegisterWork(ctx, work, command.WorkRegistration{SubjectID: "lifecycle-owner", DefinitionVersion: definition.Version, RequestID: startID}))
	require.NoError(t, requests.Send(ctx))
	require.NoError(t, submitPending(ctx, f.provisioning))
	return work, startID
}

// This single-replica fixture does not claim replica handover or telemetry
// delivery coverage. All execution feedback goes through the real Agent RPC.
type nativeLifecycleTelemetry struct{}

func newLifecycleDelivery(t *testing.T, telemetry *nativeLifecycleTelemetry) *messagedelivery.Sink {
	t.Helper()
	delivery, err := messagedelivery.NewSink(telemetry, telemetry, telemetry, slog.Default())
	require.NoError(t, err)
	t.Cleanup(func() { _ = delivery.Close(context.Background()) })
	return delivery
}

func (*nativeLifecycleTelemetry) Append(context.Context, ...*dispatcherpublicv1.UnifiedMessage) error {
	return nil
}
func (*nativeLifecycleTelemetry) PublishLive(context.Context, *dispatcherpublicv1.UnifiedMessage) error {
	return nil
}
func (*nativeLifecycleTelemetry) PublishDiagnostic(context.Context, messagedelivery.Diagnostic) error {
	return nil
}

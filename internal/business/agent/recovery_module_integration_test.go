//go:build integration

package agent_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/pkg/mysqltest"

	"github.com/talgent-work/workbench/internal/business/agent/application"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"go.uber.org/fx"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

func TestModuleRestartSettlesInterruptedAdmissionWithoutCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn := mysqltest.Database(t)
	observer, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = observer.Close() })
	port := &sessionCommandRecorder{}
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	var kube *fake.Clientset
	start := func(recover bool) (agentprivatev1connect.AgentServiceClient, func()) {
		t.Helper()
		db, err := xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		mux := http.NewServeMux()
		kube = fake.NewClientset(quota.DeepCopy())
		options := []fx.Option{agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, storageStub(),
			fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
			fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
			fx.Provide(func() kubernetes.Interface { return kube }),
			fx.Provide(func() command.InputBufferPort { return port }),
		}
		if recover {
			// Use the production recovery registration, query and handlers. Only
			// execution delivery is controlled; native transport has its own tests.
			options = append(options, agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() application.ExecutionEnvironmentRecovery { return noEnvironments{} }),
				fx.Provide(func() domain.ExecutionPort { return port }),
			)
		} else {
			options = append(options, fx.Provide(func() domain.ExecutionPort { return port }))
		}
		app := fx.New(options...)
		require.NoError(t, app.Err())
		require.NoError(t, app.Start(ctx))
		server := httptest.NewServer(mux)
		var once sync.Once
		stop := func() {
			once.Do(func() {
				server.Close()
				stopCtx, release := context.WithTimeout(context.Background(), 5*time.Second)
				defer release()
				require.NoError(t, app.Stop(stopCtx))
				require.NoError(t, db.Close())
			})
		}
		t.Cleanup(stop)
		return agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL), stop
	}
	client, stop := start(false)
	created, err := client.CreateAgentDefinition(ctx, sessionControlRequest(&agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"employee"}, AgentName: "Fixture Agent", UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "employee"}, Purpose: "work", HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "employee", Namespace: "personal"}}))
	require.NoError(t, err)
	conversation, err := client.CreateWork(ctx, sessionControlRequest(&agentprivatev1.CreateWorkRequest{SubjectId: "employee", AgentId: created.Msg.AgentId, RequestId: uuid.NewString(), WorkspaceReference: "work", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case"}, ProjectId: "project"}))
	require.NoError(t, err)
	// Complete the initial process before reproducing a Resume that was
	// rejected for capacity before its caller observed the outcome.
	workID, abandoned := conversation.Msg.WorkId, uuid.NewString()
	sessions := infrastructure.NewAgentWorkRepository(observer, time.Now)
	retainedSession, err := sessions.Get(ctx, workID)
	require.NoError(t, err)
	require.NoError(t, retainedSession.ConfirmPrepared(context.Background(), time.Now(), nil))
	require.NoError(t, retainedSession.ConfirmStarted(context.Background(), time.Now(), nil))
	require.NoError(t, retainedSession.Stop(ctx, uuid.NewString(), port))
	require.NoError(t, retainedSession.RecordExit(ctx, domain.HarnessExit{}, port))
	require.NoError(t, retainedSession.ConfirmComputeReclaimed())
	require.NoError(t, sessions.Save(ctx, retainedSession))
	retainedSession, err = sessions.Get(ctx, workID)
	require.NoError(t, err)
	require.Len(t, port.startRequests(), 1)
	continuations := infrastructure.NewContinuationRequests(observer, time.Now)
	_, err = continuations.RetainContinuation(ctx, command.ContinuationRequest{StartWork: command.StartWork{SubjectID: "employee", WorkID: workID, RequestID: abandoned}, Operation: "resume"}, retainedSession.Version)
	require.NoError(t, err)
	require.NoError(t, continuations.RecordContinuationRejection(ctx, workID, abandoned, "capacity"))
	stop()
	client, stop = start(true)
	require.Eventually(t, func() bool {
		request, found, err := continuations.GetContinuation(ctx, workID)
		return err == nil && found && request.Resolved && request.Rejection == "capacity"
	}, 5*time.Second, 20*time.Millisecond, "production continuation observation must finish the retained rejection")
	for _, action := range kube.Actions() {
		require.NotEqual(t, "resourcequotas", action.GetResource().Resource, "recovery must not request capacity again")
	}
	state, err := client.GetWork(ctx, sessionControlRequest(&agentprivatev1.GetWorkRequest{SubjectId: "employee", WorkId: workID}))
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, state.Msg.State)
	require.Len(t, port.startRequests(), 1, "recovery must not admit the abandoned request")
	_, err = client.ResumeWork(ctx, sessionControlRequest(&agentprivatev1.ResumeWorkRequest{SubjectId: "employee", WorkId: workID, RequestId: abandoned}))
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err), "the same request retains its original capacity rejection")
	require.Len(t, port.startRequests(), 1)
	next := uuid.NewString()
	_, err = client.ResumeWork(ctx, sessionControlRequest(&agentprivatev1.ResumeWorkRequest{SubjectId: "employee", WorkId: workID, RequestId: next}))
	require.NoError(t, err)
	stop()
	// The running recovery module may redeliver the resumed start while the
	// Work is Starting; that repeats one admission rather than adding another.
	requests := port.distinctStartRequests()
	require.Len(t, requests, 2, "only the explicit new request may resume the stopped Work")
	require.Equal(t, domain.ContextContinue, requests[1].ContextMode)
	for _, request := range requests {
		require.Equal(t, workID, request.WorkID)
	}
}

// Reconstruct the production module after association acknowledgement but before
// its creation-to-initial-preparation handoff. No user RPC drives recovery.
func TestModuleRestartCompletesConfirmedCreationHandoff(t *testing.T) {
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	records := infrastructure.NewWorkCreationRequests(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"employee"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "employee", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, definition))
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-342", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Recover retained explicit creation"}}
	request, err := command.NewWorkCreationRequest(command.CreateWork{RequestID: initial.RequestID, SubjectID: "employee", AgentID: definition.ID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), InitialExecution: &initial})
	require.NoError(t, err)
	request, err = records.RetainWorkCreation(ctx, request)
	require.NoError(t, err)
	port := &sessionCommandRecorder{}
	id, err := command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, infrastructure.NewWorkAuthorizer(db), agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), modulePluginCatalog{}, authorizationcatalogtest.Empty{}, sessions, computertest.Checked(moduleAvailableCapacity{}), infrastructure.NewExecutionRequestsFactory(port)).Handle(ctx, request.Creation)
	require.NoError(t, err)
	require.NoError(t, records.ConfirmWorkCreation(ctx, request.ID, id))
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	app := fx.New(agent.Module, moduleCatalog(), sessionStateTransport(t), agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() application.ExecutionEnvironmentRecovery { return noEnvironments{} }), fx.NopLogger,
		storageStub(),
		fx.Supply(http.NewServeMux(), slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(func() kubernetes.Interface { return fake.NewClientset(quota) }),
		fx.Provide(func() domain.ExecutionPort { return port }),
		fx.Provide(func() command.InputBufferPort { return port }),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() {
		stop, release := context.WithTimeout(context.Background(), 5*time.Second)
		defer release()
		require.NoError(t, app.Stop(stop))
	})
	records = infrastructure.NewWorkCreationRequests(db, time.Now)
	require.Eventually(t, func() bool {
		pending, err := records.ListPendingWorkCreations(ctx, "", 100)
		return err == nil && len(pending) == 0 && len(port.startRequests()) > 0
	}, 10*time.Second, 20*time.Millisecond)
	for _, start := range port.startRequests() {
		require.Equal(t, id, start.WorkID)
	}
	retained, err := infrastructure.NewInitialExecutionRequests(db, time.Now).Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, initial.Input, retained.Input)
	require.True(t, retained.Submitted)
	require.Eventually(t, func() bool {
		retained, err := infrastructure.NewInitialExecutionRequests(db, time.Now).Get(ctx, id)
		return err == nil && retained.InputBuffered
	}, 10*time.Second, 20*time.Millisecond, "the initial input is buffered without waiting for the Harness")
	buffered, err := port.ReadBuffer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{initial.Input.ID}, buffered.InputIDs)
}

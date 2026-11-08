//go:build integration

package agent_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
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

// A Resume commits its start with its request record at once, so nothing is
// left half admitted: a restarted module with recovery admits no Resume on
// its own, and only an explicit request resumes the stopped Work.
func TestModuleRestartAdmitsNoResumeWithoutCaller(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn := mysqltest.Database(t)
	observer, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = observer.Close() })
	port := &sessionCommandRecorder{}
	runs := &moduleRuns{}
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
			fx.Provide(func() command.DispatcherPort { return port }),
			runs.option(),
		}
		if recover {
			// Use the production recovery registration, query and handlers. Only
			// execution environments are controlled; native transport has its own tests.
			options = append(options, agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() command.ExecutionEnvironmentPort { return realizedEnvironments{} }), fx.Supply(domain.LeaseTerms{}))
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
		return agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))), stop
	}
	client, stop := start(false)
	created, err := client.CreateAgentDefinition(sessionControlRequest(ctx), &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"employee"}, AgentName: "Fixture Agent", UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "employee"}, Purpose: "work", HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "employee", Namespace: "personal"}})
	require.NoError(t, err)
	conversation, err := client.CreateWork(sessionControlRequest(ctx), &agentprivatev1.CreateWorkRequest{SubjectId: "employee", AgentId: created.AgentId, RequestId: uuid.NewString(), WorkspaceReference: "work", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "case"}, ProjectId: "project"})
	require.NoError(t, err)
	// Complete the initial process before restarting the module.
	workID := conversation.WorkId
	runs.ready(t, ctx, workID)
	stopID := uuid.NewString()
	_, err = client.StopWork(sessionControlRequest(ctx), &agentprivatev1.StopWorkRequest{SubjectId: "employee", WorkId: workID, RequestId: stopID})
	require.NoError(t, err)
	runs.awaitExitRequest(t, ctx, workID, stopID)
	runs.update(t, ctx, workID, func(e *domain.AgentExecution) bool {
		_, changed := e.RecordExit(launchedProcess, domain.HarnessExit{})
		return changed
	})
	runs.applyReports(t, ctx, workID)
	runs.release(t, ctx, workID)
	require.Eventually(t, func() bool {
		state, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "employee", WorkId: workID})
		return err == nil && state.State == agentprivatev1.WorkState_WORK_STATE_STOPPED
	}, 10*time.Second, 20*time.Millisecond, "the exited run's release stops the Work")
	require.Len(t, launches(t, observer), 1)
	stop()
	client, stop = start(true)
	// Let the restarted recovery sweep every unfinished obligation once.
	time.Sleep(500 * time.Millisecond)
	for _, action := range kube.Actions() {
		require.NotEqual(t, "resourcequotas", action.GetResource().Resource, "recovery must not request capacity again")
	}
	state, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "employee", WorkId: workID})
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, state.State)
	require.Len(t, launches(t, observer), 1, "recovery admits no Resume on its own")
	next := uuid.NewString()
	_, err = client.ResumeWork(sessionControlRequest(ctx), &agentprivatev1.ResumeWorkRequest{SubjectId: "employee", WorkId: workID, RequestId: next})
	require.NoError(t, err)
	// The running recovery module may redeliver the resumed start while the
	// Work is Starting; that repeats one admission rather than adding another.
	requests := awaitLaunches(t, observer, 2, "the explicit new request resumes the stopped Work")
	stop()
	requests = launches(t, observer)
	require.Len(t, requests, 2, "only the explicit new request may resume the stopped Work")
	require.Equal(t, domain.ContextContinue, requests[1].ContextMode)
	for _, request := range requests {
		require.Equal(t, workID, request.WorkID)
	}
}

// lostCreationAnswer commits a creation and loses its answer, so nothing
// continues the new Work but recovery.
type lostCreationAnswer struct {
	domain.AgentWorkRepository
}

var errCreationAnswerLost = errors.New("creation answer lost after the commit")

func (r lostCreationAnswer) Save(ctx context.Context, work *domain.AgentWork) error {
	registration := work.Version == 0
	if err := r.AgentWorkRepository.Save(ctx, work); err != nil || !registration {
		return err
	}
	return errCreationAnswerLost
}

// Reconstruct the production module after a creation committed but before
// anything continued it. No user RPC drives recovery: it starts the first
// start and hands the initial task input to the input buffer.
func TestModuleRestartContinuesACommittedCreation(t *testing.T) {
	dsn := mysqltest.Database(t)
	db, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	agents := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
	definition, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"employee"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "employee", Namespace: "personal"}})
	require.NoError(t, err)
	require.NoError(t, agents.Save(ctx, definition))
	initial := command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/" + "TAL-342", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Recover retained explicit creation"}}
	request := command.CreateWork{RequestID: initial.RequestID, SubjectID: "employee", AgentID: definition.ID, WorkspaceReference: uuid.NewString(), Subject: domain.WorkSubject{Kind: "case", ID: uuid.NewString()}, ProjectID: uuid.NewString(), InitialExecution: &initial}
	port := &sessionCommandRecorder{}
	models := agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkAccessReader(db))
	prepare := command.NewPrepareWorkExecutionHandler(sessions, auth, models)
	_, err = command.NewCreateWorkHandler(time.Now, agents, lostCreationAnswer{sessions}, auth, models, commandtest.StartCatalog(modulePluginCatalog{}, authorizationcatalogtest.Empty{}), computertest.Checked(moduleAvailableCapacity{}), prepare).Handle(ctx, request)
	require.ErrorIs(t, err, errCreationAnswerLost)
	committed, err := sessions.FindBySubject(ctx, definition.ID, request.Subject)
	require.NoError(t, err)
	require.NotNil(t, committed, "the creation committed")
	id := committed.ID
	require.Empty(t, launches(t, db), "nothing continued the creation before recovery")
	require.NoError(t, db.Close())
	db, err = xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	app := fx.New(agent.Module, moduleCatalog(), sessionStateTransport(t), agent.RecoveryModule, recoveryQueues(t), fx.Provide(func() command.ExecutionEnvironmentPort { return realizedEnvironments{} }), fx.Supply(domain.LeaseTerms{}), fx.NopLogger,
		storageStub(),
		fx.Supply(http.NewServeMux(), slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}), computertest.Option(&computertest.Admission{}),
		fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
		fx.Provide(func() kubernetes.Interface { return fake.NewClientset(quota) }),
		fx.Provide(func() command.DispatcherPort { return port }),
	)
	require.NoError(t, app.Err())
	require.NoError(t, app.Start(ctx))
	t.Cleanup(func() {
		stop, release := context.WithTimeout(context.Background(), 5*time.Second)
		defer release()
		require.NoError(t, app.Stop(stop))
	})
	require.Eventually(t, func() bool { return len(launches(t, db)) > 0 }, 10*time.Second, 20*time.Millisecond, "recovery begins the admitted start's execution whose Domain Event the lost answer took with it")
	for _, start := range launches(t, db) {
		require.Equal(t, id, start.WorkID)
	}
	require.Eventually(t, func() bool {
		work, err := infrastructure.NewAgentWorkRepository(db, time.Now).Get(ctx, id)
		if err != nil {
			return false
		}
		_, pending := work.PendingInitialTaskInput()
		return !pending
	}, 10*time.Second, 20*time.Millisecond, "the initial task input is handed off without waiting for the Harness")
	buffered, err := port.ReadBuffer(ctx, id)
	require.NoError(t, err)
	require.Equal(t, []string{initial.Input.ID}, buffered.InputIDs)
}

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

	"github.com/talgent-work/workbench/internal/testsupport/mysqltest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/computer"
	"go.uber.org/fx"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"xorm.io/xorm"
)

// Keep the Agent module, HTTP mapping and persistence real. Each admitted
// start begins its AgentExecution, which the test realizes as execution
// integration would. Native facts enter the feedback API; the release of a
// retired execution is confirmed as its reconciliation confirms it.
func TestSessionLifecycleThroughPrivateAPIAndModuleRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dsn := mysqltest.Database(t)
	observer, err := xorm.NewEngine("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = observer.Close() })

	port := &sessionCommandRecorder{}
	runs := &moduleRuns{}
	var recovery *command.RecoverWorkHandler
	var agentApp *application.Application
	reclaim := func(session string) {
		t.Helper()
		runs.release(t, ctx, session)
	}
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("10"), corev1.ResourceRequestsMemory: resource.MustParse("10Gi")}
	quota := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "executions", Namespace: "sandboxes"}, Spec: corev1.ResourceQuotaSpec{Hard: hard}, Status: corev1.ResourceQuotaStatus{Hard: hard.DeepCopy(), Used: corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("0"), corev1.ResourceRequestsMemory: resource.MustParse("0")}}}
	capacityClient := fake.NewClientset(quota.DeepCopy())
	startModule := func() (agentprivatev1connect.AgentServiceClient, agentprivatev1connect.ExecutionFeedbackServiceClient, func()) {
		t.Helper()
		db, err := xorm.NewEngine("mysql", dsn)
		require.NoError(t, err)
		mux := http.NewServeMux()
		server := httptest.NewServer(mux)
		app := fx.New(computer.Module, agent.Module, moduleCatalog(), sessionStateTransport(t), fx.NopLogger, storageStub(), testRedis(t),
			fx.Supply(mux, slog.Default(), agent.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, agent.ComputerConfig{Address: server.URL, Token: "computer"}, testCloud("10", "10Gi", "64")),
			fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"execution"`))),
			fx.Provide(fx.Annotate(func() *xorm.Engine { return db }, fx.ResultTags(`name:"workbench"`))),
			fx.Provide(func() kubernetes.Interface { return capacityClient }),
			fx.Provide(func() command.RunningHarnessPort { return port }),
			runs.option(),
			fx.Invoke(func(recoverSession *command.RecoverWorkHandler, app *application.Application) {
				recovery, agentApp = recoverSession, app
			}),
		)
		require.NoError(t, app.Err())
		require.NoError(t, app.Start(ctx))
		var once sync.Once
		stop := func() {
			once.Do(func() {
				server.Close()
				require.NoError(t, app.Stop(context.Background()))
				require.NoError(t, db.Close())
			})
		}
		t.Cleanup(stop)
		return agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))), agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0)))), stop
	}
	client, feedback, stop := startModule()
	created, err := client.CreateAgentDefinition(sessionControlRequest(ctx), &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"employee-a"}, AgentName: "Fixture Agent",
		UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "employee-a"}, Purpose: "work",
		HomeBinding:  &agentprivatev1.HomeBinding{OwnerId: "employee-a", Namespace: "personal"},
		Capabilities: &agentprivatev1.Capabilities{Prompt: "base prompt", StartupAgent: "custom:base", RequiredPlugins: []string{"custom"}},
	})
	require.NoError(t, err)
	definitionID := created.AgentId
	creationRequest := func(work, project string) *agentprivatev1.CreateWorkRequest {
		return &agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), SubjectId: "employee-a", AgentId: definitionID, WorkspaceReference: work, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: work}, ProjectId: project, CapabilityDifferences: &agentprivatev1.CapabilityDifferences{Prompt: proto.String("session prompt"), StartupAgent: proto.String("custom:author")}}
	}
	create := func(work, project string) string {
		t.Helper()
		response, err := client.CreateWork(sessionControlRequest(ctx), creationRequest(work, project))
		require.NoError(t, err)
		return response.WorkId
	}
	// An AgentDefinition is never one of its own Owners.
	selfOwned := creationRequest("work-self", "project-a")
	selfOwned.SubjectId = definitionID
	_, err = client.CreateWork(sessionControlRequest(ctx), selfOwned)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "the AgentDefinition identity is rejected as owner")
	require.Empty(t, launches(t, observer))
	// Initial capacity rejection must leave no conversation to resume later.
	fullQuota := quota.DeepCopy()
	fullQuota.Status.Used = hard.DeepCopy()
	_, err = capacityClient.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(ctx, fullQuota, metav1.UpdateOptions{})
	require.NoError(t, err)
	_, err = client.CreateWork(sessionControlRequest(ctx), creationRequest("work-a", "project-a"))
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Empty(t, launches(t, observer))
	_, err = capacityClient.CoreV1().ResourceQuotas("sandboxes").UpdateStatus(ctx, quota.DeepCopy(), metav1.UpdateOptions{})
	require.NoError(t, err)
	stop()
	client, feedback, stop = startModule()
	absent, err := client.LocateWork(sessionControlRequest(ctx), &agentprivatev1.LocateWorkRequest{SubjectId: "employee-a", AgentId: definitionID, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}})
	require.NoError(t, err)
	require.False(t, absent.Found)
	require.Empty(t, absent.WorkId)
	require.Empty(t, launches(t, observer), "restoring quota cannot revive a rejected creation")
	type creationResult struct {
		id  string
		err error
	}
	results := make(chan creationResult, 8)
	barrier := make(chan struct{})
	for range 8 {
		go func() {
			<-barrier
			response, err := client.CreateWork(sessionControlRequest(ctx), creationRequest("work-a", "project-a"))
			result := creationResult{err: err}
			if err == nil {
				result.id = response.WorkId
			}
			results <- result
		}()
	}
	close(barrier)
	var sessionID string
	for range 8 {
		result := <-results
		require.NoError(t, result.err)
		if sessionID == "" {
			sessionID = result.id
		}
		require.Equal(t, sessionID, result.id, "concurrent private creation must resolve one Work for the Subject")
	}
	awaitLaunches(t, observer, 1, "one Subject begins one execution")
	get := func(id string) *agentprivatev1.GetWorkResponse {
		t.Helper()
		response, err := client.GetWork(sessionControlRequest(ctx), &agentprivatev1.GetWorkRequest{SubjectId: "employee-a", WorkId: id})
		require.NoError(t, err)
		return response
	}
	original := get(sessionID)
	require.Equal(t, "session prompt", original.Capabilities.Prompt)
	require.Equal(t, "custom:author", original.Capabilities.StartupAgent)
	_, err = client.ChangeAgentDefinitionCapabilities(sessionControlRequest(ctx), &agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: definitionID, Capabilities: &agentprivatev1.Capabilities{Prompt: "changed base"}})
	require.NoError(t, err)
	require.Equal(t, sessionID, create("work-a", "project-a"), "the Subject locates its Work whatever the definition's changes")
	newInput := creationRequest("work-c", "project-c")
	newInput.CapabilityDifferences = nil
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, get(sessionID).State)
	require.Len(t, launches(t, observer), 1)

	// report delivers Dispatcher feedback about a process; the current
	// execution's is launchedProcess.
	report := func(kind agentprivatev1.FeedbackKind, processID string, exitCode int32) error {
		req := &agentprivatev1.ReportRequest{WorkId: sessionID, Kind: kind, ProcessId: processID, ExitCode: exitCode}
		ctx, info := connect.NewClientContext(ctx)
		info.RequestHeader().Set("Authorization", "Bearer feedback")
		_, err := feedback.Report(ctx, req)
		return err
	}
	// ready brings the Harness up; a fresh start keeps Working for its first
	// Turn, while a resume or restart with nothing coming waits for input.
	ready := func(want agentprivatev1.WorkState) {
		t.Helper()
		runs.realize(t, ctx, sessionID)
		require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED, "process", 0))
		require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STARTING, get(sessionID).State)
		require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED, "process", 0))
		require.Equal(t, want, get(sessionID).State)
	}
	finishStop := func(request string) {
		t.Helper()
		_, err := client.StopWork(sessionControlRequest(ctx), &agentprivatev1.StopWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: request})
		require.NoError(t, err)
		runs.awaitExitRequest(t, ctx, sessionID, request)
		require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, get(sessionID).State)
		require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, "process", 143))
		runs.awaitRetired(t, ctx, sessionID)
		require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, get(sessionID).State)
		reclaim(sessionID)
		require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, get(sessionID).State)
	}
	ready(agentprivatev1.WorkState_WORK_STATE_WORKING)
	finished := &agentprivatev1.ReportRequest{ProcessId: "process", WorkId: sessionID, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 1, TurnCompleted: true}
	finishedCtx, finishedInfo := connect.NewClientContext(ctx)
	finishedInfo.RequestHeader().Set("Authorization", "Bearer feedback")
	_, err = feedback.Report(finishedCtx, finished)
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State, "an ended root Turn leaves the Work Working")
	require.False(t, get(sessionID).GetWaiting(), "an ended root Turn is not Waiting")
	stop()
	client, feedback, stop = startModule()
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State, "the state survives module restart")
	nextInput := uuid.NewString()
	port.inputError = errors.New("input buffer unavailable")
	_, err = client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: sessionID, InputId: nextInput, Input: "continue after the result"})
	require.Error(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State, "input whose hand-off fails is not accepted")
	port.inputError = nil
	_, err = client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: sessionID, InputId: nextInput, Input: "continue after the result"})
	require.NoError(t, err, "a retry uses the same identity")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State, "buffered input waits for the Harness without changing the state")
	_, err = feedback.Report(finishedCtx, finished)
	require.NoError(t, err)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State, "a repeated old Turn end cannot erase later accepted input")
	port.settle(nextInput)
	assertWaitingResponseAdmission(t, ctx, client, feedback, port, sessionID)
	finishStop("stop-first")
	stop()
	client, feedback, stop = startModule()
	located, err := client.LocateWork(sessionControlRequest(ctx), &agentprivatev1.LocateWorkRequest{SubjectId: "employee-a", AgentId: definitionID, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}})
	require.NoError(t, err)
	require.Equal(t, sessionID, located.WorkId, "restart must retain the old binding rule")
	restored := get(sessionID)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, restored.State)
	require.True(t, proto.Equal(original.Capabilities, restored.Capabilities))
	require.Equal(t, original.ContextReference, restored.ContextReference)
	// Only the fixed owner (or a caller acting on its behalf) controls
	// the Work; neither another participant nor the AgentDefinition may.
	for _, stranger := range []string{"employee-b", definitionID} {
		_, err = client.ResumeWork(sessionControlRequest(ctx), &agentprivatev1.ResumeWorkRequest{SubjectId: stranger, WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "%s is not the Work owner", stranger)
	}
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, get(sessionID).State)
	_, err = client.ResumeWork(sessionControlRequest(ctx), &agentprivatev1.ResumeWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
	require.NoError(t, err)
	require.Equal(t, original.ContextReference, get(sessionID).ContextReference)
	ready(agentprivatev1.WorkState_WORK_STATE_WORKING)
	// The late exit fact of the stopped process names that process and cannot
	// end the resumed one; applying the running execution's reports again
	// changes nothing.
	require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, "earlier", 0))
	runs.applyReports(t, ctx, sessionID)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get(sessionID).State)
	require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, "process", 1))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_FAILED, get(sessionID).State, "an exit without an admitted stop is a failure")
	require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, "process", 1))
	reclaim(sessionID)
	_, err = client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-b", WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "Restart is admitted only for the owner")
	_, err = client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
	require.NoError(t, err)
	require.Equal(t, original.ContextReference, get(sessionID).ContextReference)
	ready(agentprivatev1.WorkState_WORK_STATE_WORKING)
	inputID := uuid.NewString()
	_, err = client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: sessionID, InputId: inputID, Input: "finish before close"})
	require.NoError(t, err)
	_, err = client.DeliverInput(sessionControlRequest(ctx), &agentprivatev1.DeliverInputRequest{SubjectId: "employee-a", WorkId: sessionID, InputId: inputID, Input: "conflicting content"})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = client.CloseWork(sessionControlRequest(ctx), &agentprivatev1.CloseWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: "close"})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "production module must fence close against admitted input")
	port.settle(inputID)
	completedInput := &agentprivatev1.ReportRequest{ProcessId: "process", WorkId: sessionID, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_INPUT_SETTLED, InputId: inputID, InputOutcome: agentprivatev1.InputOutcome_INPUT_OUTCOME_COMPLETED}
	completedInputCtx, completedInputInfo := connect.NewClientContext(ctx)
	completedInputInfo.RequestHeader().Set("Authorization", "Bearer feedback")
	_, err = feedback.Report(completedInputCtx, completedInput)
	require.NoError(t, err)
	endedTurn := &agentprivatev1.ReportRequest{ProcessId: "process", WorkId: sessionID, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_TURN_FINISHED, ObservationSequence: 20, TurnCompleted: true}
	endedTurnCtx, endedTurnInfo := connect.NewClientContext(ctx)
	endedTurnInfo.RequestHeader().Set("Authorization", "Bearer feedback")
	_, err = feedback.Report(endedTurnCtx, endedTurn)
	require.NoError(t, err)
	accepted, err := client.CloseWork(sessionControlRequest(ctx), &agentprivatev1.CloseWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: "close"})
	require.NoError(t, err)
	require.Equal(t, sessionID, accepted.WorkId)
	require.Equal(t, "close", accepted.RequestId)
	runs.awaitExitRequest(t, ctx, sessionID, "close")
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, get(sessionID).State, "a running Work is stopped before it closes")
	require.NoError(t, recovery.Handle(ctx, sessionID))
	rejectExecution := func() {
		t.Helper()
		_, err := client.ResumeWork(sessionControlRequest(ctx), &agentprivatev1.ResumeWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
		_, err = client.RestartWork(sessionControlRequest(ctx), &agentprivatev1.RestartWorkRequest{SubjectId: "employee-a", WorkId: sessionID, RequestId: uuid.Must(uuid.NewV7()).String()})
		require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	}
	rejectExecution()
	require.NoError(t, report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, "process", 0))
	runs.awaitRetired(t, ctx, sessionID)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPING, get(sessionID).State)
	reclaim(sessionID)
	// Once stopped, the Work's recovery, which its execution-ended event wakes
	// and the sweep finds, applies the kept close.
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, get(sessionID).State)
	require.NoError(t, application.NewRecovery(agentApp, nil, nil, nil, nil, nil, nil).RecoverWork(ctx, sessionID))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, get(sessionID).State)
	stop()
	client, feedback, _ = startModule()
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_CLOSED, get(sessionID).State)
	rejectExecution()
	requests := launches(t, observer)
	require.Len(t, requests, 3, "closure must not begin any new execution")
	for _, request := range requests {
		require.Equal(t, domain.HomeBinding{OwnerID: "employee-a", Namespace: "personal"}, request.HomeBinding)
		require.Equal(t, "session prompt", request.Capabilities.Prompt)
		require.Equal(t, "custom:author", request.Capabilities.StartupAgent)
		require.Equal(t, sessionID, request.WorkID)
	}
	require.Equal(t, domain.ContextContinue, requests[1].ContextMode)
	require.Equal(t, requests[0].HarnessSessionReference, requests[1].HarnessSessionReference)
	require.Equal(t, domain.ContextRecover, requests[2].ContextMode)
	require.Equal(t, requests[1].HarnessSessionReference, requests[2].HarnessSessionReference)
	absent, err = client.LocateWork(sessionControlRequest(ctx), &agentprivatev1.LocateWorkRequest{SubjectId: "employee-a", AgentId: definitionID, Subject: newInput.Subject})
	require.NoError(t, err)
	require.False(t, absent.Found, "a Subject without a Work locates none")
	require.Empty(t, absent.WorkId)
	newSession, err := client.CreateWork(sessionControlRequest(ctx), newInput)
	require.NoError(t, err, "no definition limits how many of its Works run")
	require.Equal(t, "changed base", get(newSession.WorkId).Capabilities.Prompt)
	require.NotEqual(t, original.ContextReference, get(newSession.WorkId).ContextReference)
	requests = awaitLaunches(t, observer, 4, "the new Work begins its execution")
	require.Equal(t, requests[0].HomeBinding, requests[3].HomeBinding, "the employee HOME is shared across projects")
	require.NotEqual(t, requests[0].HarnessSessionReference, requests[3].HarnessSessionReference)
}

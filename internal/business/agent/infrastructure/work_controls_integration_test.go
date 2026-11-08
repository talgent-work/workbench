//go:build integration

package infrastructure_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	"github.com/talgent-work/contracts/gen/go/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	dispatchercommand "github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/internal/testsupport/connecttest"
	"xorm.io/xorm"
)

type workCapacity struct{ available bool }

func (c *workCapacity) AdmitRoom(context.Context) error {
	if !c.available {
		return domain.ErrCapacityUnavailable
	}
	return nil
}

func TestRestartStartupRetriesRetainedInitialInput(t *testing.T) {
	f := newWorkCreationFixture(t)
	ctx := context.Background()
	auth := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(f.db).WorkOwners)
	id := f.committedCreation(t)
	capacity := &workCapacity{available: true}
	ref := id
	feedback := newRuns(t, f.db, f.sessions)
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref}))
	}
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: ref, ProcessID: "process", ExitCode: 1, ExitReason: "native_initialization_failed"}))
	feedback.release(ctx, ref)
	reader := infrastructure.NewWorkQueryRepository(f.db)
	failed, err := reader.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "failed", failed.StartupProgress.Status)
	require.Equal(t, "workspace", failed.StartupProgress.Stages[2].Stage)

	_, err = workRef(id)(command.NewRestartWorkHandler(time.Now, f.sessions, f.agents, auth, computertest.Checked(capacity), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}).Handle(ctx, command.StartWork{SubjectID: "owner", WorkID: id, RequestID: uuid.NewString()}))
	require.NoError(t, err)
	starting, err := reader.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "preparing", starting.StartupProgress.Status)
	require.Empty(t, starting.StartupProgress.Reason)
	require.Len(t, starting.StartupProgress.Stages, 5, "Restart retains workspace preparation and the original undelivered input")
	for _, kind := range []command.ExecutionFeedbackKind{command.ExecutorPrepared, command.AgentStarted} {
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: id}))
	}
	ready, err := reader.GetWorkView(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "working", ready.State)
	require.Equal(t, "preparing", ready.StartupProgress.Status, "Harness readiness does not prove original input delivery")
	require.Equal(t, "completed", ready.StartupProgress.Stages[2].Status, "the execution's preparation ended the repository's checkout")
	require.Equal(t, "active", ready.StartupProgress.Stages[4].Status)
	require.Equal(t, failed.StartupRequest.InitialMessage, ready.StartupRequest.InitialMessage, "Restart retains the original input")
	work, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	_, pending := work.PendingInitialTaskInput()
	require.True(t, pending, "Restart must not invent delivery of the retained input")
}

// The database contains Agent tables only. Execution is supplied at its
// external boundary; SQL, public HTTP and lifecycle handlers are real. Agent
// admits only the Work's owner from its own facts and asks no caller.
func TestOwnerWorkViewAndControlsUseOnlyAgentFacts(t *testing.T) {
	const owner = "123456789012345680"
	const viewer = "123456789012345679"
	f := newWorkDefinitionFixture(t, owner)
	ctx := context.Background()
	var err error
	id := f.committedCreation(t)
	capacity := &workCapacity{available: true}
	clientFor := func(db *xorm.Engine) agentpublicv1connect.AgentWorkServiceClient {
		principal := access.NewWorkAuthorizer(infrastructure.NewWorkQueryRepository(db).WorkOwners)
		sessions := infrastructure.NewAgentWorkRepository(db, time.Now)
		definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
		reader := infrastructure.NewWorkQueryRepository(db)
		app := &application.Application{WorkPrincipals: principal, WorkQueries: reader, Commands: application.Commands{
			StopWork:    command.NewStopWorkHandler(sessions, principal, executionsOf(db)),
			ResumeWork:  command.NewResumeWorkHandler(time.Now, sessions, definitions, principal, computertest.Checked(capacity), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			RestartWork: command.NewRestartWorkHandler(time.Now, sessions, definitions, principal, computertest.Checked(capacity), querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			CloseWork:   command.NewCloseWorkHandler(sessions, principal, executionsOf(db), &commandtest.InputBuffer{}),
		}, Queries: application.Queries{GetWorkView: query.NewGetWorkViewHandler(principal, reader)}}
		handler := connecttest.Handler(func(s *connect.Server) {
			agentpublicv1connect.RegisterAgentWorkServiceHandler(s, transport.NewWorkHandler(app))
		}, authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true}))
		server := httptest.NewServer(handler)
		t.Cleanup(server.Close)
		return agentpublicv1connect.NewAgentWorkServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	}
	client := clientFor(f.db)
	read := func(state string) *agentpublicv1.WorkInfo {
		result, err := client.GetWork(workMessageRequest(ctx, owner), &agentpublicv1.GetWorkRequest{WorkId: id})
		require.NoError(t, err)
		require.Equal(t, state, result.Work.State)
		require.Equal(t, id, result.Work.WorkId)
		return result.Work
	}
	created := read("starting")
	require.False(t, created.VisibilityPublic, "an employee's Work starts private")
	require.False(t, created.CreatedAt.AsTime().IsZero())
	_, err = client.GetWork(workMessageRequest(ctx, viewer), &agentpublicv1.GetWorkRequest{WorkId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "another member reads collaboration facts through the caller")
	_, err = client.GetWork(ctx, &agentpublicv1.GetWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.ArchiveWork(workMessageRequest(ctx, owner), &agentpublicv1.ArchiveWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	ref := id
	feedback := newRuns(t, f.db, f.sessions)
	var feedbackSequence int64 = 1000
	report := func(kind command.ExecutionFeedbackKind, interaction string) {
		feedbackSequence++
		var required []string
		if kind == command.UserInteractionRequired {
			required = []string{interaction}
		}
		require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{ProcessID: "process", Kind: kind, WorkID: ref, InteractionID: interaction, ObservationSequence: feedbackSequence, RequiredInteractionIDs: required}))
	}
	startingView := read("starting")
	require.Equal(t, "active", startingView.StartupProgress.Stages[1].Status)
	require.Equal(t, "sandbox", startingView.StartupProgress.Stages[1].Stage)
	report(command.ExecutorPrepared, "")
	report(command.AgentStarted, "")
	runningView := read("working")
	inputView := runningView
	require.Equal(t, "workspace", inputView.StartupProgress.Stages[2].Stage)
	require.Equal(t, "completed", inputView.StartupProgress.Stages[2].Status, "preparation ends with an outcome for every repository")
	require.Equal(t, "not_checked_out", inputView.StartupRequest.Repositories[0].CheckoutStatus, "a repository the preparation did not report is degraded, not retried")
	require.Equal(t, "active", inputView.StartupProgress.Stages[4].Status)
	require.Greater(t, inputView.StartupProgress.Revision, startingView.StartupProgress.Revision)
	require.NoError(t, command.NewInputAcceptance(f.sessions, &commandtest.InputBuffer{}).HandOffInitialTaskInput(ctx, id))
	completeView := read("working")
	require.Equal(t, "completed", completeView.StartupProgress.Status)
	require.Greater(t, completeView.StartupProgress.Revision, inputView.StartupProgress.Revision)
	feedbackHTTP := connecttest.Handler(func(s *connect.Server) {
		agentprivatev1connect.RegisterExecutionFeedbackServiceHandler(s, transport.NewFeedbackHandler(&application.Application{Commands: application.Commands{ApplyExecutionFeedback: feedback.feedback}}, nil, "progress-test"))
	})
	feedbackServer := httptest.NewServer(feedbackHTTP)
	t.Cleanup(feedbackServer.Close)
	reporter := agentfeedback.NewClient(feedbackServer.Client(), feedbackServer.URL, "progress-test")
	// Captured native tool results pass through the production normalizer,
	// feedback RPC, Agent storage and public query in one connected boundary.
	sequence := int64(10)
	var protocol nativeprocess.ProtocolState
	for _, fixture := range []string{"work_progress_claude_2_1_266.json", "todo_progress_claude_2_1_266.json"} {
		raw, err := os.ReadFile("../../dispatcher/infrastructure/nativeprocess/testdata/" + fixture)
		require.NoError(t, err)
		var frames []nativeprocess.Message
		require.NoError(t, json.Unmarshal(raw, &frames))
		for _, frame := range frames {
			items, updated := protocol.ObserveWorkProgress(&frame)
			if !updated {
				continue
			}
			sequence++
			require.NoError(t, reporter.ReportExecution(ctx, dispatchercommand.ExecutionObservation{WorkID: id, ProcessID: "process", Kind: dispatchercommand.ExecutionWorkProgressUpdated, ObservationSequence: sequence, WorkProgress: items}))
		}
		if fixture == "work_progress_claude_2_1_266.json" {
			require.NotNil(t, read("working").WorkProgress)
			require.Empty(t, read("working").WorkProgress.Items, "TaskList's final empty snapshot replaces the recorded task")
		}
	}
	progressView := read("working")
	require.NotNil(t, progressView.WorkProgress)
	require.Len(t, progressView.WorkProgress.Items, 1)
	require.Equal(t, "Verify Work", progressView.WorkProgress.Items[0].Content)
	require.Equal(t, "in_progress", progressView.WorkProgress.Items[0].Status)
	require.Positive(t, runningView.Version)
	require.Greater(t, progressView.Version, runningView.Version)
	_, err = client.StopWork(workMessageRequest(ctx, owner), &agentpublicv1.StopWorkRequest{WorkId: id, ExpectedVersion: &runningView.Version})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "an obsolete Work view must not stop the current process")
	read("working")
	for _, actor := range []string{viewer, messageCollaborator} {
		_, err = client.StopWork(workMessageRequest(ctx, actor), &agentpublicv1.StopWorkRequest{WorkId: id})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "only the owner controls its Work")
	}
	report(command.UserInteractionRequired, "question")
	require.True(t, read("working").Waiting)
	waiting, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	report(command.UserInteractionRequired, "question")
	repeated, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, waiting.HarnessProcess.WaitingSince, repeated.HarnessProcess.WaitingSince, "a repeated request keeps the Waiting origin")
	stop, err := client.StopWork(workMessageRequest(ctx, owner), &agentpublicv1.StopWorkRequest{WorkId: id})
	require.NoError(t, err)
	require.Equal(t, "stopping", stop.Work.State)
	stopping, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	_, err = client.StopWork(workMessageRequest(ctx, owner), &agentpublicv1.StopWorkRequest{WorkId: id})
	require.NoError(t, err)
	current, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, stopping.StopRequestReference, current.StopRequestReference)
	report(command.ExecutionExited, "")
	feedback.release(ctx, ref)
	stoppedView := read("stopped")
	require.Equal(t, []string{"resume", "archive"}, stoppedView.AllowedActions)
	before, err := f.sessions.Get(ctx, id)
	require.NoError(t, err)
	_, err = client.ResumeWork(workMessageRequest(ctx, messageCollaborator), &agentpublicv1.ResumeWorkRequest{WorkId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	capacity.available = false
	_, err = client.ResumeWork(workMessageRequest(ctx, owner), &agentpublicv1.ResumeWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	rejected := read("stopped")
	require.False(t, rejected.ResumePending)
	require.Equal(t, stoppedView.Version, rejected.Version, "a refused Resume retains nothing on the Work")
	require.Equal(t, stoppedView.StartupProgress.GetStatus(), rejected.StartupProgress.GetStatus(), "a refused Resume answers its caller and shows no start")
	require.Equal(t, stoppedView.StartupProgress.GetRequestId(), rejected.StartupProgress.GetRequestId())
	require.Equal(t, stoppedView.StartupProgress.GetRevision(), rejected.StartupProgress.GetRevision())
	capacity.available = true
	other, err := xorm.NewEngine("mysql", f.dsn)
	require.NoError(t, err)
	defer other.Close()
	client = clientFor(other)
	progressAfterReconnect, err := client.GetWork(workMessageRequest(ctx, owner), &agentpublicv1.GetWorkRequest{WorkId: id})
	require.NoError(t, err)
	require.Equal(t, progressView.WorkProgress.Items[0].Content, progressAfterReconnect.Work.WorkProgress.Items[0].Content)
	require.Equal(t, progressView.WorkProgress.UpdatedAt.AsTime(), progressAfterReconnect.Work.WorkProgress.UpdatedAt.AsTime())
	stoppedView = read("stopped")
	_, err = client.ResumeWork(workMessageRequest(ctx, owner), &agentpublicv1.ResumeWorkRequest{WorkId: id, ExpectedVersion: &runningView.Version})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "an obsolete Work view must not resume the Work")
	require.False(t, read("stopped").ResumePending)
	// Admission accepts the version the caller observed.
	resumed, err := client.ResumeWork(workMessageRequest(ctx, owner), &agentpublicv1.ResumeWorkRequest{WorkId: id, ExpectedVersion: &stoppedView.Version})
	require.NoError(t, err)
	require.Equal(t, "starting", resumed.Work.State)
	require.Len(t, resumed.Work.StartupProgress.Stages, 3, "normal Resume reuses the completed initial preparation")
	require.Greater(t, resumed.Work.StartupProgress.Revision, rejected.StartupProgress.Revision)
	require.True(t, resumed.Work.ResumePending, "completed admission still awaits execution readiness")
	current, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before.HarnessSessionReference, current.HarnessSessionReference)
	require.Equal(t, before.WorkspaceReference, current.WorkspaceReference)
	require.Equal(t, before.CapabilitySnapshot, current.CapabilitySnapshot)
	require.Equal(t, before.Subject, current.Subject)
	// Failed startup requires explicit Restart with native context recovery. Neither Resume nor
	// reading recovery facts can select it, and no one but the owner can request it.
	require.NoError(t, feedback.Handle(ctx, command.ExecutionFeedback{Kind: command.ExecutionExited, WorkID: ref, ProcessID: "process", ExitCode: 1, ExitReason: "native_initialization_failed"}))
	feedback.release(ctx, ref)
	failed := read("failed")
	require.Equal(t, "failed", failed.StartupProgress.Status)
	_, err = client.ResumeWork(workMessageRequest(ctx, owner), &agentpublicv1.ResumeWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	for _, actor := range []string{viewer, messageCollaborator} {
		_, err = client.RestartWork(workMessageRequest(ctx, actor), &agentpublicv1.RestartWorkRequest{WorkId: id})
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	}
	capacity.available = false
	_, err = client.RestartWork(workMessageRequest(ctx, owner), &agentpublicv1.RestartWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	failedView := read("failed")
	require.Equal(t, failed.Version, failedView.Version, "a refused Restart retains nothing on the Work")
	capacity.available = true
	_, err = client.RestartWork(workMessageRequest(ctx, owner), &agentpublicv1.RestartWorkRequest{WorkId: id, ExpectedVersion: &stoppedView.Version})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "an obsolete Work view must not restart the Work")
	read("failed")
	restarted, err := client.RestartWork(workMessageRequest(ctx, owner), &agentpublicv1.RestartWorkRequest{WorkId: id, ExpectedVersion: &failedView.Version})
	require.NoError(t, err)
	require.Equal(t, id, restarted.Work.WorkId)
	require.Equal(t, "starting", restarted.Work.State)
	current, err = f.sessions.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, before.HarnessSessionReference, current.HarnessSessionReference)
	require.Equal(t, before.WorkspaceReference, current.WorkspaceReference)
	require.Equal(t, before.CapabilitySnapshot, current.CapabilitySnapshot)
	require.Equal(t, before.HomeBinding, current.HomeBinding)
	report(command.ExecutorPrepared, "")
	report(command.AgentStarted, "")
	require.False(t, read("working").ResumePending)
	stopped, err := client.StopWork(workMessageRequest(ctx, owner), &agentpublicv1.StopWorkRequest{WorkId: id})
	require.NoError(t, err)
	require.Equal(t, "stopping", stopped.Work.State)
	report(command.ExecutionExited, "")
	feedback.release(ctx, ref)
	_, err = client.ArchiveWork(workMessageRequest(ctx, messageCollaborator), &agentpublicv1.ArchiveWorkRequest{WorkId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = client.ArchiveWork(workMessageRequest(ctx, owner), &agentpublicv1.ArchiveWorkRequest{WorkId: id, ExpectedVersion: &failedView.Version})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "an obsolete Work view must not archive the Work")
	archivable := read("stopped")
	archived, err := client.ArchiveWork(workMessageRequest(ctx, owner), &agentpublicv1.ArchiveWorkRequest{WorkId: id, ExpectedVersion: &archivable.Version})
	require.NoError(t, err)
	require.Equal(t, "closed", archived.Work.State)
	require.False(t, archived.Work.ArchivePending)
	_, err = client.ArchiveWork(workMessageRequest(ctx, owner), &agentpublicv1.ArchiveWorkRequest{WorkId: id})
	require.NoError(t, err)
	_, err = client.ResumeWork(workMessageRequest(ctx, owner), &agentpublicv1.ResumeWorkRequest{WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	read("closed")
}

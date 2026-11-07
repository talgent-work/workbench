package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/contracts/gen/go/agent/private/v1"
	"github.com/talgent-work/contracts/gen/go/agent/private/v1/agentprivatev1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestAgentRPCControlsPreserveLifecycleAuthority(t *testing.T) {
	ctx := context.Background()
	observationTime := time.Date(2026, 9, 11, 12, 0, 0, 123000000, time.UTC)
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	sessions := &rpcSessions{items: map[string]*domain.AgentWork{}}
	capacity := &computertest.Admission{}
	permissions := &rpcAuthorization{}
	port := &rpcExecution{}
	buffer := &commandtest.InputBuffer{}
	events, executions, coordination := executionWorld(time.Now, sessions, capacity, buffer)
	sessions.events = events
	app := &application.Application{Definitions: agents, Works: sessions,
		Commands: application.Commands{
			CreateWork:             command.NewCreateWorkHandler(time.Now, agents, sessions, permissions, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(rpcPluginCatalog{}, authorizationcatalogtest.Empty{}), capacity),
			ResumeWork:             command.NewResumeWorkHandler(time.Now, sessions, agents, permissions, capacity, &rpcContinuations{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			RestartWork:            command.NewRestartWorkHandler(time.Now, sessions, agents, permissions, capacity, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			StopWork:               command.NewStopWorkHandler(sessions, permissions, executions),
			CloseWork:              command.NewCloseWorkHandler(sessions, permissions, executions, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, buffer),
			DeliverInput:           command.NewDeliverWorkInputHandler(sessions, permissions, port, rpcInitialInputs{}),
			RespondToInteraction:   command.NewRespondWorkInteractionHandler(sessions, permissions, port),
			ApplyExecutionFeedback: command.NewApplyExecutionFeedbackHandler(func() time.Time { return observationTime }, sessions, executions, coordination, buffer, &commandtest.InputSettlements{}),
		}, Queries: application.Queries{LocateWork: query.NewLocateWorkHandler(agents, sessions, permissions), GetStartAdmission: query.NewGetStartAdmissionHandler(sessions, permissions), GetAgentDefinition: query.NewGetAgentDefinitionHandler(agents, application.AssembleAgentDefinition), GetWork: query.NewGetWorkHandler(agents, sessions, permissions, application.AssembleAgentWorkEntity, querytest.NoRequiredResponse)},
	}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	feedback := agentprivatev1connect.NewExecutionFeedbackServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	create := &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "owner", Namespace: "project"}, Capabilities: &agentprivatev1.Capabilities{Prompt: "review", StartupAgent: "code-review:architect", RequiredPlugins: []string{"code-review"}}}
	_, err := client.CreateAgentDefinition(ctx, create)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.CreateAgentDefinition(authenticated(ctx, "feedback"), create)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, agents.items)
	created, err := client.CreateAgentDefinition(authenticated(ctx, "control"), create)
	require.NoError(t, err)

	_, err = client.CreateWork(authenticated(ctx, "untrusted-caller"), &agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "intruder", AgentId: created.GetAgentId(), WorkspaceReference: "workspace"})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, sessions.items)
	createSession := &agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "owner", AgentId: created.GetAgentId(), WorkspaceReference: "workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case"}}
	_, err = client.CreateWork(authenticated(ctx, "control"), createSession)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a Subject names its object")
	require.Empty(t, sessions.items)
	createSession.Subject = nil
	_, err = client.CreateWork(authenticated(ctx, "control"), createSession)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a Work with neither a Subject nor a Project is a Personal Work, which no caller creates")
	createSession.ProjectId, createSession.ReadOnlyMounts = "project-a", []*agentprivatev1.ReadOnlyMount{{Name: "inputs", Reference: "space"}}
	_, err = client.CreateWork(authenticated(ctx, "control"), createSession)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a read-only mount never takes the input source's place")
	require.Empty(t, sessions.items)
	createSession.ReadOnlyMounts = nil
	createSession.Subject, createSession.ProjectId = &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}, "project-a"
	capacity.SetFull(true)
	_, err = client.CreateWork(authenticated(ctx, "control"), createSession)
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Empty(t, sessions.items)
	require.Empty(t, sessions.starts)
	require.Empty(t, executions.launches())
	capacity.SetFull(false)
	require.Empty(t, executions.launches(), "restored capacity does not execute a rejected creation")
	session, err := client.CreateWork(authenticated(ctx, "control"), createSession)
	require.NoError(t, err)
	require.NotEmpty(t, session.GetWorkId())
	located, err := client.LocateWork(authenticated(ctx, "control"), &agentprivatev1.LocateWorkRequest{SubjectId: "owner", AgentId: created.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}})
	require.NoError(t, err)
	require.True(t, located.GetFound())
	require.Equal(t, session.GetWorkId(), located.GetWorkId())
	absent, err := client.LocateWork(authenticated(ctx, "control"), &agentprivatev1.LocateWorkRequest{SubjectId: "owner", AgentId: created.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "missing"}})
	require.NoError(t, err)
	require.False(t, absent.GetFound())
	require.Empty(t, absent.GetWorkId())
	_, err = client.LocateWork(authenticated(ctx, "control"), &agentprivatev1.LocateWorkRequest{SubjectId: "intruder", AgentId: created.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Len(t, sessions.items, 1, "lookup must not create another conversation")
	require.Equal(t, domain.WorkStarting, sessions.items[session.GetWorkId()].State)
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "deployment-default"}, sessions.items[session.GetWorkId()].WorkModel, "the private creation entry must also capture the deployment model")

	changed := &agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: created.GetAgentId(), Capabilities: &agentprivatev1.Capabilities{Prompt: "new review instructions", StartupAgent: "code-review:reviewer", RequiredPlugins: []string{"code-review"}}}
	_, err = client.ChangeAgentDefinitionCapabilities(authenticated(ctx, "feedback"), changed)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.ChangeAgentDefinitionCapabilities(authenticated(ctx, "control"), changed)
	require.NoError(t, err)
	definition, err := client.GetAgentDefinition(authenticated(ctx, "control"), &agentprivatev1.GetAgentDefinitionRequest{AgentId: created.GetAgentId()})
	require.NoError(t, err)
	require.Equal(t, "new review instructions", definition.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", definition.GetCapabilities().GetStartupAgent())
	require.Equal(t, []string{"code-review"}, definition.GetCapabilities().GetRequiredPlugins())
	newer, err := client.CreateWork(authenticated(ctx, "control"), &agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "owner", AgentId: created.GetAgentId(), WorkspaceReference: "another-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-b"}, ProjectId: "project-a"})
	require.NoError(t, err)
	newerSnapshot, err := client.GetWork(authenticated(ctx, "control"), &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: newer.GetWorkId()})
	require.NoError(t, err)
	require.Equal(t, "new review instructions", newerSnapshot.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", newerSnapshot.GetCapabilities().GetStartupAgent())
	require.Equal(t, "original", newerSnapshot.GetCapabilities().GetPlugins()[0].GetConfiguration().AsMap()["mode"])
	_, err = client.ChangeAgentDefinitionCapabilities(authenticated(ctx, "control"), &agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: created.GetAgentId(), Capabilities: &agentprivatev1.Capabilities{RequiredPlugins: []string{"duplicate", "duplicate"}}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	definition, err = client.GetAgentDefinition(authenticated(ctx, "control"), &agentprivatev1.GetAgentDefinitionRequest{AgentId: created.GetAgentId()})
	require.NoError(t, err)
	require.Equal(t, "new review instructions", definition.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", definition.GetCapabilities().GetStartupAgent())
	// Presence must survive the wire: an explicit empty prompt differs from omission.
	emptyPrompt, startup := "", "extra:reviewer"
	override, err := client.CreateWork(authenticated(ctx, "control"), &agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(),
		SubjectId: "owner", AgentId: created.GetAgentId(),
		WorkspaceReference: "override-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-override"}, ProjectId: "project-a",
		CapabilityDifferences: &agentprivatev1.CapabilityDifferences{Prompt: &emptyPrompt, StartupAgent: &startup, Plugins: []*agentprivatev1.PluginConfiguration{{Name: "code-review", Configuration: &structpb.Struct{}}, {Name: "extra", Configuration: &structpb.Struct{Fields: map[string]*structpb.Value{"language": structpb.NewStringValue("zh")}}}}},
	})
	require.NoError(t, err)
	overrideSnapshot, err := client.GetWork(authenticated(ctx, "control"), &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: override.GetWorkId()})
	require.NoError(t, err)
	require.Empty(t, overrideSnapshot.GetCapabilities().GetPrompt())
	require.Equal(t, "extra:reviewer", overrideSnapshot.GetCapabilities().GetStartupAgent())
	require.Len(t, overrideSnapshot.GetCapabilities().GetPlugins(), 2)
	require.Equal(t, map[string]any{"mode": "original"}, overrideSnapshot.GetCapabilities().GetPlugins()[0].GetConfiguration().AsMap())
	require.Equal(t, "extra", overrideSnapshot.GetCapabilities().GetPlugins()[1].GetRelease().GetSlug())
	require.Equal(t, map[string]any{"language": "zh"}, overrideSnapshot.GetCapabilities().GetPlugins()[1].GetConfiguration().AsMap())
	id := session.GetWorkId()
	request := &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: id}
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "intruder", WorkId: id})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Starting Work needs no Resume")
	require.Len(t, executions.launches(), 3)
	state := func(want agentprivatev1.WorkState) *agentprivatev1.GetWorkResponse {
		result, err := client.GetWork(authenticated(ctx, "control"), request)
		require.NoError(t, err)
		require.Equal(t, want, result.GetState())
		require.Equal(t, "workspace", result.GetWorkspaceReference())
		require.Equal(t, int64(sessions.items[id].Version), result.GetVersion())
		require.Positive(t, result.GetVersion())
		return result
	}
	state(agentprivatev1.WorkState_WORK_STATE_STARTING)
	startRequest := createSession.RequestId
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: startRequest, SubjectId: "owner", WorkId: id})
	require.Error(t, err, "the creation's request identity cannot be reused by another operation")
	require.Len(t, executions.launches(), 3, "the retained creation receipt prevents another admission")

	admissionRequest := &agentprivatev1.GetStartAdmissionRequest{SubjectId: "owner", WorkId: id, RequestId: startRequest}
	admission, err := client.GetStartAdmission(authenticated(ctx, "control"), admissionRequest)
	require.NoError(t, err)
	require.True(t, admission.Accepted)
	require.Equal(t, agentprivatev1.StartKind_START_KIND_START, admission.Kind)
	_, err = client.GetStartAdmission(authenticated(ctx, "wrong-token"), admissionRequest)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.GetStartAdmission(authenticated(ctx, "control"), &agentprivatev1.GetStartAdmissionRequest{SubjectId: "intruder", WorkId: id, RequestId: startRequest})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	missing, err := client.GetStartAdmission(authenticated(ctx, "control"), &agentprivatev1.GetStartAdmissionRequest{SubjectId: "owner", WorkId: id, RequestId: uuid.NewString()})
	require.NoError(t, err)
	require.False(t, missing.Accepted)
	require.Equal(t, agentprivatev1.StartKind_START_KIND_UNSPECIFIED, missing.Kind)
	report := func(kind agentprivatev1.FeedbackKind) {
		_, err := feedback.Report(authenticated(ctx, "feedback"), &agentprivatev1.ReportRequest{WorkId: id, Kind: kind, ProcessId: launchedProcess})
		require.NoError(t, err)
	}
	// released confirms the release of the Work's retired execution.
	released := func() { executions.release(t, id) }
	// reportExit delivers an exit fact naming a process; "process" is current.
	reportExit := func(processID string, code int32) {
		_, err := feedback.Report(authenticated(ctx, "feedback"), &agentprivatev1.ReportRequest{WorkId: id, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, ProcessId: processID, ExitCode: code})
		require.NoError(t, err)
	}
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	state(agentprivatev1.WorkState_WORK_STATE_STARTING)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	running := state(agentprivatev1.WorkState_WORK_STATE_WORKING)
	require.Equal(t, "review", running.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:architect", running.GetCapabilities().GetStartupAgent())
	require.Equal(t, "original", running.GetCapabilities().GetPlugins()[0].GetConfiguration().AsMap()["mode"])
	input := &agentprivatev1.DeliverInputRequest{SubjectId: "owner", WorkId: id, InputId: "f28f3171-1268-4f97-b7d2-1ac831099012", Input: "user task"}
	_, err = client.DeliverInput(authenticated(ctx, "control"), input)
	require.NoError(t, err)
	_, err = client.DeliverInput(authenticated(ctx, "control"), input)
	require.NoError(t, err)
	require.Equal(t, []command.ExecutionInput{{ID: input.InputId, Text: input.Input}, {ID: input.InputId, Text: input.Input}}, port.inputs)
	input.InputId = ""
	_, err = client.DeliverInput(authenticated(ctx, "control"), input)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.Len(t, port.inputs, 2)
	_, err = feedback.Report(authenticated(ctx, "feedback"), &agentprivatev1.ReportRequest{ProcessId: "process", ObservationSequence: 1000, RequiredInteractionIds: []string{"question"}, WorkId: id, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, InteractionId: "question"})
	require.NoError(t, err)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_WORKING).GetWaiting())
	answer := &agentprivatev1.RespondToInteractionRequest{SubjectId: "owner", WorkId: id, InteractionId: "question", Allow: true, AlwaysAllow: true, Reason: "approved", UpdatedInputJson: `{"answer":"yes"}`}
	_, err = client.RespondToInteraction(authenticated(ctx, "feedback"), answer)
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	answer.SubjectId = "intruder"
	_, err = client.RespondToInteraction(authenticated(ctx, "control"), answer)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Empty(t, port.responses)
	answer.SubjectId = "owner"
	_, err = client.RespondToInteraction(authenticated(ctx, "control"), answer)
	require.NoError(t, err)
	require.Equal(t, []command.InteractionResponse{{InteractionID: "question", Allow: true, AlwaysAllow: true, Reason: "approved", UpdatedInputJSON: `{"answer":"yes"}`}}, port.responses)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_WORKING).GetWaiting(), "this port records the reply without the Work accepting it")
	answer.InteractionId = "unrelated"
	_, err = client.RespondToInteraction(authenticated(ctx, "control"), answer)
	require.NoError(t, err)
	require.Len(t, port.responses, 2, "Dispatcher owns request matching")
	_, err = client.StopWork(authenticated(ctx, "control"), &agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stop"})
	require.NoError(t, err)
	require.Equal(t, "stop", state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetStopRequestId())
	require.Equal(t, "user_stopped", state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetStopReason())
	require.Equal(t, []string{"stop"}, executions.exitRequests())
	reportExit("process", 0)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetWaiting(), "the stopped Work still owes the answer")
	require.Equal(t, 1, executions.retired())
	released()
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	resumed, err := client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.NoError(t, err)
	require.Equal(t, id, resumed.GetWorkId())
	require.Equal(t, running.GetContextReference(), executions.launches()[3].HarnessSessionReference, "the resumed process continues the Work's Harness Session")
	require.Equal(t, domain.ContextContinue, executions.launches()[3].ContextMode)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	// A late exit fact of the earlier run names that process and changes nothing.
	reportExit("earlier", 0)
	observed := state(agentprivatev1.WorkState_WORK_STATE_WORKING)
	require.Equal(t, 1, executions.retired())
	staleVersion := observed.GetVersion() - 1
	_, err = client.StopWork(authenticated(ctx, "control"), &agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stale-stop", ExpectedVersion: &staleVersion})
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "a stop from a stale view is rejected")
	require.Equal(t, []string{"stop"}, executions.exitRequests())
	currentVersion := observed.GetVersion()
	_, err = client.StopWork(authenticated(ctx, "control"), &agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stop-again", ExpectedVersion: &currentVersion})
	require.NoError(t, err)
	// With the stop admitted, the process's end completes the stop normally.
	reportExit("process", 1)
	released()
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.NoError(t, err)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	// An unrequested end of the running process is a failure.
	reportExit("process", 1)
	state(agentprivatev1.WorkState_WORK_STATE_FAILED)
	released()
	restarted, err := client.RestartWork(authenticated(ctx, "control"), &agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.NoError(t, err)
	require.Equal(t, id, restarted.GetWorkId())
	require.Equal(t, running.GetContextReference(), executions.launches()[5].HarnessSessionReference)
	require.Equal(t, domain.ContextRecover, executions.launches()[5].ContextMode)
	require.Equal(t, executions.launches()[0].Capabilities, executions.launches()[5].Capabilities)
	require.Equal(t, executions.launches()[0].HomeBinding, executions.launches()[5].HomeBinding)
	_, err = client.CloseWork(authenticated(ctx, "control"), &agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a starting Work is not closed")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	closed, err := client.CloseWork(authenticated(ctx, "control"), &agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"})
	require.NoError(t, err)
	require.Equal(t, id, closed.GetWorkId())
	require.Equal(t, "close", closed.GetRequestId())
	state(agentprivatev1.WorkState_WORK_STATE_STOPPING)
	_, err = client.RestartWork(authenticated(ctx, "control"), &agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	reportExit("process", 0)
	released()
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Work being closed is not resumed")
	// The kept close applies once stopped; this fixture repeats it directly.
	_, err = client.CloseWork(authenticated(ctx, "control"), &agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"})
	require.NoError(t, err)
	state(agentprivatev1.WorkState_WORK_STATE_CLOSED)
	_, err = client.ResumeWork(authenticated(ctx, "control"), &agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	_, err = client.RestartWork(authenticated(ctx, "control"), &agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id})
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Len(t, executions.launches(), 6)
}

// authenticated is the context of a call under the control token.
func authenticated(ctx context.Context, token string) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	return ctx
}

func TestDefinitionRPCPreservesAuthorizedScopeAndHome(t *testing.T) {
	ctx := context.Background()
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	app := &application.Application{Definitions: agents, Queries: application.Queries{GetAgentDefinition: query.NewGetAgentDefinitionHandler(agents, application.AssembleAgentDefinition)}}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL, connecthttp.WithReadMaxBytes(0))))
	request := &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work",
		UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "personal-home", Namespace: "personal"},
		Capabilities: &agentprivatev1.Capabilities{StartupAgent: "employees:member"},
	}
	created, err := client.CreateAgentDefinition(authenticated(ctx, "control"), request)
	require.NoError(t, err)
	current, err := client.GetAgentDefinition(authenticated(ctx, "control"), &agentprivatev1.GetAgentDefinitionRequest{AgentId: created.GetAgentId()})
	require.NoError(t, err)
	require.Equal(t, "employee", current.GetUsageScope().GetKind())
	require.Equal(t, "owner", current.GetUsageScope().GetReference())
	require.Equal(t, "personal-home", current.GetHomeBinding().GetOwnerId())
	require.Equal(t, "personal", current.GetHomeBinding().GetNamespace())
	request.UsageScope = nil
	_, err = client.CreateAgentDefinition(authenticated(ctx, "control"), request)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a definition cannot be created without an explicit usage scope")
	request.UsageScope = &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}
	request.HomeBinding = nil
	_, err = client.CreateAgentDefinition(authenticated(ctx, "control"), request)
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "HOME belongs to the definition")
}

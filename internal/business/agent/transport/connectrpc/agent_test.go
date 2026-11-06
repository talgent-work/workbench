package connectrpc_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentmodule "github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/application/query/querytest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
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
	processes := &rpcProcesses{observation: domain.ProcessObservation{State: domain.ProcessPending}}
	requests := infrastructure.NewExecutionRequestsFactory(port)
	app := &application.Application{Definitions: agents, Works: sessions,
		Commands: application.Commands{
			CreateWork:             command.NewCreateWorkHandler(time.Now, agents, sessions, sessions, permissions, query.NewModelConfigurations(query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), rpcPluginCatalog{}, authorizationcatalogtest.Empty{}, sessions, capacity, requests),
			ResumeWork:             command.NewResumeWorkHandler(time.Now, sessions, agents, permissions, capacity, requests, sessions, &rpcContinuations{}, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			RestartWork:            command.NewRestartWorkHandler(time.Now, sessions, agents, permissions, capacity, requests, sessions, querytest.ProvidedModels(), commandtest.UnchangedPlugins{}),
			StopWork:               command.NewStopWorkHandler(sessions, permissions, requests),
			CloseWork:              command.NewCloseWorkHandler(sessions, permissions, requests, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, &commandtest.InputBuffer{}),
			DeliverInput:           command.NewDeliverWorkInputHandler(sessions, permissions, port, rpcInitialInputs{}),
			RespondToInteraction:   command.NewRespondWorkInteractionHandler(sessions, permissions, port),
			ApplyExecutionFeedback: command.NewApplyExecutionFeedbackHandler(func() time.Time { return observationTime }, sessions, processes, requests, &commandtest.InputBuffer{}, &commandtest.InputSettlements{}),
		}, Queries: application.Queries{LocateWork: query.NewLocateWorkHandler(agents, sessions, permissions), GetStartAdmission: query.NewGetStartAdmissionHandler(sessions, permissions), GetAgentDefinition: query.NewGetAgentDefinitionHandler(agents, application.AssembleAgentDefinition), GetWork: query.NewGetWorkHandler(agents, sessions, permissions, application.AssembleAgentWorkEntity, querytest.NoRequiredResponse)},
	}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL)
	feedback := agentprivatev1connect.NewExecutionFeedbackServiceClient(server.Client(), server.URL)
	create := &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "owner", Namespace: "project"}, Capabilities: &agentprivatev1.Capabilities{Prompt: "review", StartupAgent: "code-review:architect", RequiredPlugins: []string{"code-review"}}}
	_, err := client.CreateAgentDefinition(ctx, connect.NewRequest(create))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.CreateAgentDefinition(ctx, authenticated(create, "feedback"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, agents.items)
	created, err := client.CreateAgentDefinition(ctx, authenticated(create, "control"))
	require.NoError(t, err)

	_, err = client.CreateWork(ctx, authenticated(&agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "intruder", AgentId: created.Msg.GetAgentId(), WorkspaceReference: "workspace"}, "untrusted-caller"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	require.Empty(t, sessions.items)
	createSession := &agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "owner", AgentId: created.Msg.GetAgentId(), WorkspaceReference: "workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case"}}
	_, err = client.CreateWork(ctx, authenticated(createSession, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a Subject names its object")
	require.Empty(t, sessions.items)
	createSession.Subject = nil
	_, err = client.CreateWork(ctx, authenticated(createSession, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a Work with neither a Subject nor a Project is a Personal Work, which no caller creates")
	createSession.ProjectId, createSession.ReadOnlyMounts = "project-a", []*agentprivatev1.ReadOnlyMount{{Name: "inputs", Reference: "space"}}
	_, err = client.CreateWork(ctx, authenticated(createSession, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a read-only mount never takes the input source's place")
	require.Empty(t, sessions.items)
	createSession.ReadOnlyMounts = nil
	createSession.Subject, createSession.ProjectId = &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}, "project-a"
	capacity.SetFull(true)
	_, err = client.CreateWork(ctx, authenticated(createSession, "control"))
	require.Equal(t, connect.CodeResourceExhausted, connect.CodeOf(err))
	require.Empty(t, sessions.items)
	require.Empty(t, sessions.starts)
	require.Empty(t, port.starts)
	capacity.SetFull(false)
	require.Empty(t, port.starts, "restored capacity does not execute a rejected creation")
	session, err := client.CreateWork(ctx, authenticated(createSession, "control"))
	require.NoError(t, err)
	require.NotEmpty(t, session.Msg.GetWorkId())
	located, err := client.LocateWork(ctx, authenticated(&agentprivatev1.LocateWorkRequest{SubjectId: "owner", AgentId: created.Msg.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}}, "control"))
	require.NoError(t, err)
	require.True(t, located.Msg.GetFound())
	require.Equal(t, session.Msg.GetWorkId(), located.Msg.GetWorkId())
	absent, err := client.LocateWork(ctx, authenticated(&agentprivatev1.LocateWorkRequest{SubjectId: "owner", AgentId: created.Msg.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "missing"}}, "control"))
	require.NoError(t, err)
	require.False(t, absent.Msg.GetFound())
	require.Empty(t, absent.Msg.GetWorkId())
	_, err = client.LocateWork(ctx, authenticated(&agentprivatev1.LocateWorkRequest{SubjectId: "intruder", AgentId: created.Msg.GetAgentId(), Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-a"}}, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Len(t, sessions.items, 1, "lookup must not create another conversation")
	require.Equal(t, domain.WorkStarting, sessions.items[session.Msg.GetWorkId()].State)
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "deployment-default"}, sessions.items[session.Msg.GetWorkId()].WorkModel, "the private creation entry must also capture the deployment model")

	changed := &agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: created.Msg.GetAgentId(), Capabilities: &agentprivatev1.Capabilities{Prompt: "new review instructions", StartupAgent: "code-review:reviewer", RequiredPlugins: []string{"code-review"}}}
	_, err = client.ChangeAgentDefinitionCapabilities(ctx, authenticated(changed, "feedback"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.ChangeAgentDefinitionCapabilities(ctx, authenticated(changed, "control"))
	require.NoError(t, err)
	definition, err := client.GetAgentDefinition(ctx, authenticated(&agentprivatev1.GetAgentDefinitionRequest{AgentId: created.Msg.GetAgentId()}, "control"))
	require.NoError(t, err)
	require.Equal(t, "new review instructions", definition.Msg.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", definition.Msg.GetCapabilities().GetStartupAgent())
	require.Equal(t, []string{"code-review"}, definition.Msg.GetCapabilities().GetRequiredPlugins())
	newer, err := client.CreateWork(ctx, authenticated(&agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(), SubjectId: "owner", AgentId: created.Msg.GetAgentId(), WorkspaceReference: "another-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-b"}, ProjectId: "project-a"}, "control"))
	require.NoError(t, err)
	newerSnapshot, err := client.GetWork(ctx, authenticated(&agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: newer.Msg.GetWorkId()}, "control"))
	require.NoError(t, err)
	require.Equal(t, "new review instructions", newerSnapshot.Msg.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", newerSnapshot.Msg.GetCapabilities().GetStartupAgent())
	require.Equal(t, "original", newerSnapshot.Msg.GetCapabilities().GetPlugins()[0].GetConfiguration().AsMap()["mode"])
	_, err = client.ChangeAgentDefinitionCapabilities(ctx, authenticated(&agentprivatev1.ChangeAgentDefinitionCapabilitiesRequest{AgentId: created.Msg.GetAgentId(), Capabilities: &agentprivatev1.Capabilities{RequiredPlugins: []string{"duplicate", "duplicate"}}}, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	definition, err = client.GetAgentDefinition(ctx, authenticated(&agentprivatev1.GetAgentDefinitionRequest{AgentId: created.Msg.GetAgentId()}, "control"))
	require.NoError(t, err)
	require.Equal(t, "new review instructions", definition.Msg.GetCapabilities().GetPrompt())
	require.Equal(t, "code-review:reviewer", definition.Msg.GetCapabilities().GetStartupAgent())
	// Presence must survive the wire: an explicit empty prompt differs from omission.
	emptyPrompt, startup := "", "extra:reviewer"
	override, err := client.CreateWork(ctx, authenticated(&agentprivatev1.CreateWorkRequest{StartingUserId: "owner", RequestId: uuid.NewString(),
		SubjectId: "owner", AgentId: created.Msg.GetAgentId(),
		WorkspaceReference: "override-workspace", Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "work-override"}, ProjectId: "project-a",
		CapabilityDifferences: &agentprivatev1.CapabilityDifferences{Prompt: &emptyPrompt, StartupAgent: &startup, Plugins: []*agentprivatev1.PluginConfiguration{{Name: "code-review", Configuration: &structpb.Struct{}}, {Name: "extra", Configuration: &structpb.Struct{Fields: map[string]*structpb.Value{"language": structpb.NewStringValue("zh")}}}}},
	}, "control"))
	require.NoError(t, err)
	overrideSnapshot, err := client.GetWork(ctx, authenticated(&agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: override.Msg.GetWorkId()}, "control"))
	require.NoError(t, err)
	require.Empty(t, overrideSnapshot.Msg.GetCapabilities().GetPrompt())
	require.Equal(t, "extra:reviewer", overrideSnapshot.Msg.GetCapabilities().GetStartupAgent())
	require.Len(t, overrideSnapshot.Msg.GetCapabilities().GetPlugins(), 2)
	require.Equal(t, map[string]any{"mode": "original"}, overrideSnapshot.Msg.GetCapabilities().GetPlugins()[0].GetConfiguration().AsMap())
	require.Equal(t, "extra", overrideSnapshot.Msg.GetCapabilities().GetPlugins()[1].GetRelease().GetSlug())
	require.Equal(t, map[string]any{"language": "zh"}, overrideSnapshot.Msg.GetCapabilities().GetPlugins()[1].GetConfiguration().AsMap())
	id := session.Msg.GetWorkId()
	request := &agentprivatev1.GetWorkRequest{SubjectId: "owner", WorkId: id}
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "intruder", WorkId: id}, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Starting Work needs no Resume")
	require.Len(t, port.starts, 3)
	state := func(want agentprivatev1.WorkState) *agentprivatev1.GetWorkResponse {
		result, err := client.GetWork(ctx, authenticated(request, "control"))
		require.NoError(t, err)
		require.Equal(t, want, result.Msg.GetState())
		require.Equal(t, "workspace", result.Msg.GetWorkspaceReference())
		require.Equal(t, int64(sessions.items[id].Version), result.Msg.GetVersion())
		require.Positive(t, result.Msg.GetVersion())
		return result.Msg
	}
	state(agentprivatev1.WorkState_WORK_STATE_STARTING)
	startRequest := createSession.RequestId
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: startRequest, SubjectId: "owner", WorkId: id}, "control"))
	require.Error(t, err, "the creation's request identity cannot be reused by another operation")
	require.Len(t, port.starts, 3, "the retained creation receipt prevents another admission")

	admissionRequest := &agentprivatev1.GetStartAdmissionRequest{SubjectId: "owner", WorkId: id, RequestId: startRequest}
	admission, err := client.GetStartAdmission(ctx, authenticated(admissionRequest, "control"))
	require.NoError(t, err)
	require.True(t, admission.Msg.Accepted)
	require.Equal(t, agentprivatev1.StartKind_START_KIND_START, admission.Msg.Kind)
	_, err = client.GetStartAdmission(ctx, authenticated(admissionRequest, "wrong-token"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = client.GetStartAdmission(ctx, authenticated(&agentprivatev1.GetStartAdmissionRequest{SubjectId: "intruder", WorkId: id, RequestId: startRequest}, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	missing, err := client.GetStartAdmission(ctx, authenticated(&agentprivatev1.GetStartAdmissionRequest{SubjectId: "owner", WorkId: id, RequestId: uuid.NewString()}, "control"))
	require.NoError(t, err)
	require.False(t, missing.Msg.Accepted)
	require.Equal(t, agentprivatev1.StartKind_START_KIND_UNSPECIFIED, missing.Msg.Kind)
	report := func(kind agentprivatev1.FeedbackKind) {
		if kind == agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED {
			processes.observation = domain.ProcessObservation{State: domain.ProcessPending, ComputeReclaimed: true}
		}
		_, err := feedback.Report(ctx, authenticated(&agentprivatev1.ReportRequest{WorkId: id, Kind: kind, ProcessId: "process"}, "feedback"))
		require.NoError(t, err)
	}
	// reportExit delivers an exit fact naming a process; "process" is current.
	reportExit := func(processID string, code int32) {
		_, err := feedback.Report(ctx, authenticated(&agentprivatev1.ReportRequest{WorkId: id, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_EXITED, ProcessId: processID, ExitCode: code}, "feedback"))
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
	_, err = client.DeliverInput(ctx, authenticated(input, "control"))
	require.NoError(t, err)
	_, err = client.DeliverInput(ctx, authenticated(input, "control"))
	require.NoError(t, err)
	require.Equal(t, []command.ExecutionInput{{ID: input.InputId, Text: input.Input}, {ID: input.InputId, Text: input.Input}}, port.inputs)
	input.InputId = ""
	_, err = client.DeliverInput(ctx, authenticated(input, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	require.Len(t, port.inputs, 2)
	_, err = feedback.Report(ctx, authenticated(&agentprivatev1.ReportRequest{ProcessId: "process", ObservationSequence: 1000, RequiredInteractionIds: []string{"question"}, WorkId: id, Kind: agentprivatev1.FeedbackKind_FEEDBACK_KIND_USER_INTERACTION_REQUIRED, InteractionId: "question"}, "feedback"))
	require.NoError(t, err)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_WORKING).GetWaiting())
	answer := &agentprivatev1.RespondToInteractionRequest{SubjectId: "owner", WorkId: id, InteractionId: "question", Allow: true, AlwaysAllow: true, Reason: "approved", UpdatedInputJson: `{"answer":"yes"}`}
	_, err = client.RespondToInteraction(ctx, authenticated(answer, "feedback"))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	answer.SubjectId = "intruder"
	_, err = client.RespondToInteraction(ctx, authenticated(answer, "control"))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Empty(t, port.responses)
	answer.SubjectId = "owner"
	_, err = client.RespondToInteraction(ctx, authenticated(answer, "control"))
	require.NoError(t, err)
	require.Equal(t, []command.InteractionResponse{{InteractionID: "question", Allow: true, AlwaysAllow: true, Reason: "approved", UpdatedInputJSON: `{"answer":"yes"}`}}, port.responses)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_WORKING).GetWaiting(), "this port records the reply without the Work accepting it")
	answer.InteractionId = "unrelated"
	_, err = client.RespondToInteraction(ctx, authenticated(answer, "control"))
	require.NoError(t, err)
	require.Len(t, port.responses, 2, "Dispatcher owns request matching")
	_, err = client.StopWork(ctx, authenticated(&agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stop"}, "control"))
	require.NoError(t, err)
	require.Equal(t, "stop", state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetStopRequestId())
	require.Equal(t, "user_stopped", state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetStopReason())
	require.Equal(t, []string{"stop"}, port.stops)
	reportExit("process", 0)
	require.True(t, state(agentprivatev1.WorkState_WORK_STATE_STOPPING).GetWaiting(), "the stopped Work still owes the answer")
	require.Len(t, port.reclaims, 1)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED)
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	resumed, err := client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.NoError(t, err)
	require.Equal(t, id, resumed.Msg.GetWorkId())
	require.Equal(t, running.GetContextReference(), port.starts[3].HarnessSessionReference, "the resumed process continues the Work's Harness Session")
	require.Equal(t, domain.ContextContinue, port.starts[3].ContextMode)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	// A late exit fact of the earlier run names that process and changes nothing.
	processes.observation = domain.ProcessObservation{State: domain.ProcessRunning}
	reportExit("earlier", 0)
	observed := state(agentprivatev1.WorkState_WORK_STATE_WORKING)
	require.Len(t, port.reclaims, 1)
	staleVersion := observed.GetVersion() - 1
	_, err = client.StopWork(ctx, authenticated(&agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stale-stop", ExpectedVersion: &staleVersion}, "control"))
	require.Equal(t, connect.CodeAborted, connect.CodeOf(err), "a stop from a stale view is rejected")
	require.Equal(t, []string{"stop"}, port.stops)
	currentVersion := observed.GetVersion()
	_, err = client.StopWork(ctx, authenticated(&agentprivatev1.StopWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "stop-again", ExpectedVersion: &currentVersion}, "control"))
	require.NoError(t, err)
	// With the stop admitted, the process's end completes the stop normally.
	reportExit("process", 1)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED)
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.NoError(t, err)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	// An unrequested end of the running process is a failure.
	reportExit("process", 1)
	state(agentprivatev1.WorkState_WORK_STATE_FAILED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED)
	restarted, err := client.RestartWork(ctx, authenticated(&agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.NoError(t, err)
	require.Equal(t, id, restarted.Msg.GetWorkId())
	require.Equal(t, running.GetContextReference(), port.starts[5].HarnessSessionReference)
	require.Equal(t, domain.ContextRecover, port.starts[5].ContextMode)
	require.Equal(t, port.starts[0].Capabilities, port.starts[5].Capabilities)
	require.Equal(t, port.starts[0].HomeBinding, port.starts[5].HomeBinding)
	_, err = client.CloseWork(ctx, authenticated(&agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a starting Work is not closed")
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_PREPARED)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_STARTED)
	closed, err := client.CloseWork(ctx, authenticated(&agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"}, "control"))
	require.NoError(t, err)
	require.Equal(t, id, closed.Msg.GetWorkId())
	require.Equal(t, "close", closed.Msg.GetRequestId())
	state(agentprivatev1.WorkState_WORK_STATE_STOPPING)
	_, err = client.RestartWork(ctx, authenticated(&agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	reportExit("process", 0)
	report(agentprivatev1.FeedbackKind_FEEDBACK_KIND_COMPUTE_RECLAIMED)
	state(agentprivatev1.WorkState_WORK_STATE_STOPPED)
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a Work being closed is not resumed")
	// The kept close applies once stopped; this fixture repeats it directly.
	_, err = client.CloseWork(ctx, authenticated(&agentprivatev1.CloseWorkRequest{SubjectId: "owner", WorkId: id, RequestId: "close"}, "control"))
	require.NoError(t, err)
	state(agentprivatev1.WorkState_WORK_STATE_CLOSED)
	_, err = client.ResumeWork(ctx, authenticated(&agentprivatev1.ResumeWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	_, err = client.RestartWork(ctx, authenticated(&agentprivatev1.RestartWorkRequest{RequestId: uuid.Must(uuid.NewV7()).String(), SubjectId: "owner", WorkId: id}, "control"))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	require.Len(t, port.starts, 6)
}

func authenticated[T any](message *T, token string) *connect.Request[T] {
	req := connect.NewRequest(message)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

func TestDefinitionRPCPreservesAuthorizedScopeAndHome(t *testing.T) {
	ctx := context.Background()
	agents := &rpcAgents{items: map[string]*domain.AgentDefinition{}}
	app := &application.Application{Definitions: agents, Queries: application.Queries{GetAgentDefinition: query.NewGetAgentDefinitionHandler(agents, application.AssembleAgentDefinition)}}
	mux := http.NewServeMux()
	agentmodule.RegisterRPCHandlers(mux, app, agentmodule.RPCConfig{ControlToken: "control", FeedbackToken: "feedback"}, slog.Default(), nil)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := agentprivatev1connect.NewAgentServiceClient(server.Client(), server.URL)
	request := &agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"owner"}, AgentName: "Fixture Agent", Purpose: "work",
		UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "personal-home", Namespace: "personal"},
		Capabilities: &agentprivatev1.Capabilities{StartupAgent: "employees:member"},
	}
	created, err := client.CreateAgentDefinition(ctx, authenticated(request, "control"))
	require.NoError(t, err)
	current, err := client.GetAgentDefinition(ctx, authenticated(&agentprivatev1.GetAgentDefinitionRequest{AgentId: created.Msg.GetAgentId()}, "control"))
	require.NoError(t, err)
	require.Equal(t, "employee", current.Msg.GetUsageScope().GetKind())
	require.Equal(t, "owner", current.Msg.GetUsageScope().GetReference())
	require.Equal(t, "personal-home", current.Msg.GetHomeBinding().GetOwnerId())
	require.Equal(t, "personal", current.Msg.GetHomeBinding().GetNamespace())
	request.UsageScope = nil
	_, err = client.CreateAgentDefinition(ctx, authenticated(request, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a definition cannot be created without an explicit usage scope")
	request.UsageScope = &agentprivatev1.UsageScope{Kind: "employee", Reference: "owner"}
	request.HomeBinding = nil
	_, err = client.CreateAgentDefinition(ctx, authenticated(request, "control"))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "HOME belongs to the definition")
}

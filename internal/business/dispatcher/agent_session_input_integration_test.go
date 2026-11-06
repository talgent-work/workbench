//go:build integration

package dispatcher_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/private/v1/agentprivatev1connect"
	agentpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/agent/public/v1/agentpublicv1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1/dispatcherprivatev1connect"
	agentapp "github.com/talgent-work/workbench/internal/business/agent/application"
	agentcommand "github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	agentdomain "github.com/talgent-work/workbench/internal/business/agent/domain"
	agentinfra "github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	agenttransport "github.com/talgent-work/workbench/internal/business/agent/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/agentfeedback"
	transport "github.com/talgent-work/workbench/internal/business/dispatcher/transport/connectrpc"
	"github.com/talgent-work/workbench/internal/testsupport/computertest"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/nativetest"
	"xorm.io/xorm"
)

// Real Agent API/authorization/storage, Dispatcher API/checkpoint and native I/O.
// Only inference and environment provisioning are controlled: this is messaging
// acceptance, not a deployed OpenSandbox/Kubernetes lifecycle claim.
// Closing a running Work is Agent's own sequence over the same stop
// (TestCloseStopsARunningWorkAndClosesItOnceStopped), so only the stop runs here.
func TestNativeDispatcherCarriesAgentSessionInputAndReplies(t *testing.T) {
	program := os.Getenv(talgentComputerProgram)
	var execdBinary, claudeBinary string
	if program != "" {
		claudeBinary = nativetest.Require(t, "CLAUDE_BINARY")[0]
	} else {
		required := nativetest.Require(t, "EXECD_BINARY", "CLAUDE_BINARY")
		execdBinary, claudeBinary = required[0], required[1]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	model := &nativeInference{}
	inference := httptest.NewServer(model)
	defer inference.Close()
	root := t.TempDir()
	skillPath := filepath.Join(root, ".claude", "skills", "entry", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillPath), 0700))
	require.NoError(t, os.WriteFile(skillPath, []byte("---\nname: entry\ndescription: Native entry\nargument-hint: \"[value]\"\n---\nNATIVE_SLASH_ARGS=$ARGUMENTS\n"), 0600))
	processClient, endpoint := nativeMessagingExecd(t, ctx, root, execdBinary, claudeBinary, program, inference.URL)
	if program != "" {
		claudeBinary = managedHarness
	}
	db := agentWorkDB(t)
	agents := agentinfra.NewAgentDefinitionRepository(db, time.Now)
	sessions := agentinfra.NewAgentWorkRepository(db, time.Now)
	auth := agentinfra.NewWorkAuthorizer(db)
	app := &agentapp.Application{}
	agentMux := http.NewServeMux()
	path, handler := agentprivatev1connect.NewAgentServiceHandler(agenttransport.NewAgentHandler(app, "control-test"))
	agentMux.Handle(path, handler)
	path, handler = agentprivatev1connect.NewExecutionFeedbackServiceHandler(agenttransport.NewFeedbackHandler(app, nil, "feedback-test"))
	agentMux.Handle(path, handler)
	agentServer := httptest.NewServer(agentMux)
	defer agentServer.Close()
	records := &nativeRecords{}

	feedback := &nativeSessionFeedback{nativeRecords: records, target: agentfeedback.NewClient(agentServer.Client(), agentServer.URL, "feedback-test")}
	replicas := newNativeReplicas(t)
	manager := replicas.replica("replica", feedback, newTestDelivery(t, records, records))
	buffer := replicas.inputs()
	dispatchApp := application.NewApplication(manager, command.NewConnectExecutionHandler(manager), command.NewEnqueueInputHandler(buffer), command.NewDropInputHandler(buffer), command.NewStopExecutionHandler(manager), manager, manager, manager, manager, buffer)
	_, dispatchHandler := dispatcherprivatev1connect.NewExecutionServiceHandler(transport.NewExecutionHandler(dispatchApp, "control-test"))
	dispatchServer := httptest.NewServer(dispatchHandler)
	defer dispatchServer.Close()
	dispatcherClient := dispatcherprivatev1connect.NewExecutionServiceClient(dispatchServer.Client(), dispatchServer.URL)
	messaging := execution.NewDispatcherControl(dispatchServer.Client(), dispatchServer.URL, "control-test")
	resources := &nativeMessageResources{client: processClient, dispatcher: dispatcherClient, binary: claudeBinary, root: root, endpoint: endpoint}
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		for _, id := range resources.processIDs {
			_ = processClient.Delete(cleanup, id)
		}
	}()
	environment, err := execution.NewEnvironment(resources, messaging, grantedMounts{}, cloudOnly{})
	require.NoError(t, err)
	requests := agentinfra.NewExecutionRequestsFactory(environment)
	app.Definitions = agents
	app.Works = sessions
	app.Turns = messaging
	app.Capabilities = messaging
	app.PluginResults = agentinfra.NewPluginResults(db, time.Now)
	app.WorkPrincipals = auth
	app.WorkViews = agentinfra.NewWorkQueryRepository(db)
	app.Interactions = messaging
	publicPath, publicHandler := agentpublicv1connect.NewAgentWorkServiceHandler(agenttransport.NewWorkHandler(app), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	agentMux.Handle(publicPath, publicHandler)
	interactionClient := agentpublicv1connect.NewAgentWorkServiceClient(agentServer.Client(), agentServer.URL)
	pendingFor := func(workID, actor string) (*agentpublicv1.ListPendingInteractionsResponse, error) {
		request := connect.NewRequest(&agentpublicv1.ListPendingInteractionsRequest{WorkId: workID})
		request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header().Set(authn.HeaderSubject, actor)
		response, err := interactionClient.ListPendingInteractions(ctx, request)
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	requiredResponse := func(ctx context.Context, ref string) (bool, error) {
		pending, err := messaging.ListPendingInteractions(ctx, ref)
		for _, item := range pending {
			if !item.ReplySubmitted {
				return true, err
			}
		}
		return false, err
	}
	admittedMessaging := agentcommand.NewInputAcceptance(sessions, sessions, messaging)
	app.Commands = agentapp.Commands{
		CreateWork:             agentcommand.NewCreateWorkHandler(time.Now, agents, sessions, sessions, auth, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: agentdomain.WorkModelSelection{ConfigurationID: "deployment-default"}}), nativeMessageCatalog{}, authorizationcatalogtest.Empty{}, sessions, computertest.Checked(nativeMessageCapacity{}), requests),
		StopWork:               agentcommand.NewStopWorkHandler(sessions, auth, requests),
		CloseWork:              agentcommand.NewCloseWorkHandler(sessions, auth, requests, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, commandtest.NoClosureObligations{}, messaging),
		DeliverInput:           agentcommand.NewDeliverWorkInputHandler(sessions, auth, admittedMessaging, agentinfra.NewInitialExecutionRequests(db, time.Now)),
		RespondToInteraction:   agentcommand.NewRespondWorkInteractionHandler(sessions, auth, admittedMessaging),
		ApplyExecutionFeedback: agentcommand.NewApplyExecutionFeedbackHandler(time.Now, sessions, nativeProcessObservation{manager: manager, resources: resources}, requests, messaging, &commandtest.InputSettlements{}),
	}
	app.Commands.SendMessage = agentcommand.NewSendWorkMessageHandler(app.WorkPrincipals, app.Commands.DeliverInput)
	app.Queries.GetWork = agentquery.NewGetWorkHandler(agents, sessions, auth, agentapp.AssembleAgentWorkEntity, requiredResponse)
	client := agentprivatev1connect.NewAgentServiceClient(agentServer.Client(), agentServer.URL)
	created, err := client.CreateAgentDefinition(ctx, nativeRequest(&agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"e6c7e756-007d-5236-a492-7f4b9be0b2e8"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: &agentprivatev1.UsageScope{Kind: "employee", Reference: "e6c7e756-007d-5236-a492-7f4b9be0b2e8"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", Namespace: "personal"}, Capabilities: &agentprivatev1.Capabilities{}}))
	require.NoError(t, err)
	conversation, err := client.CreateWork(ctx, nativeRequest(&agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), AgentId: created.Msg.AgentId, Subject: &agentprivatev1.WorkSubject{Kind: "case", Id: "fixture-case"}, ProjectId: "fixture-project", SubjectId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", WorkspaceReference: root}))
	require.NoError(t, err)
	sessionID := conversation.Msg.WorkId

	records.awaitWork(t, sessionID, command.ExecutionStarted)
	get := func() *agentprivatev1.GetWorkResponse {
		t.Helper()
		result, err := client.GetWork(ctx, nativeRequest(&agentprivatev1.GetWorkRequest{WorkId: sessionID, SubjectId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8"}))
		require.NoError(t, err)
		return result.Msg
	}
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_WORKING, get().State)
	assertNativeWorkCapabilities(t, ctx, interactionClient, sessionID)
	inputID := uuid.NewString()
	const slashInput = "/entry native-agent-entry-marker --strict"
	send := func(subject, id, text string) error {
		request := connect.NewRequest(&agentpublicv1.SendMessageRequest{WorkId: sessionID, InputId: id, Text: text})
		request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header().Set(authn.HeaderSubject, subject)
		_, err := interactionClient.SendMessage(ctx, request)
		return err
	}
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(send("2445ddc9-704b-55f5-a37a-c517b514e080", uuid.NewString(), "forbidden-owner")))
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", inputID, slashInput))
	pending := records.awaitWork(t, sessionID, command.ExecutionUserInteractionRequired)
	require.True(t, get().RequiredResponse)
	pendingItems, err := pendingFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	require.NoError(t, err)
	require.Len(t, pendingItems.Interactions, 1)
	require.Equal(t, pending.InteractionID, pendingItems.Interactions[0].Id)
	require.Contains(t, pendingItems.Interactions[0].QuestionInputJson, "Proceed?")
	_, err = pendingFor(sessionID, "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", inputID, slashInput), "replaying an accepted input is a repeat without new work")
	// Steer behind a pending question is accepted; the Harness holds it until
	// the question is answered and joins it to the running Turn.
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", uuid.NewString(), "steer-behind-question"))
	answer := &agentprivatev1.RespondToInteractionRequest{SubjectId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", WorkId: sessionID, InteractionId: "obsolete", Allow: true, UpdatedInputJson: `{"questions":[{"question":"Proceed?","header":"Choice","options":[{"label":"Yes","description":"Proceed"},{"label":"No","description":"Stop"}],"multiSelect":false}],"answers":{"Proceed?":"Yes"}}`}
	_, err = client.RespondToInteraction(ctx, nativeRequest(answer))
	require.NoError(t, err, "whether the interaction exists is judged at delivery")
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == "obsolete" && fact.InputOutcome == command.InputClosed {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "a reply to an interaction that does not exist settles as closed")
	answer.InteractionId = pending.InteractionID
	_, err = client.RespondToInteraction(ctx, nativeRequest(answer))
	require.NoError(t, err)
	_, err = client.RespondToInteraction(ctx, nativeRequest(answer))
	require.NoError(t, err)
	records.awaitWork(t, sessionID, command.ExecutionUserInteractionResolved)
	records.awaitResult(t, slashInput)
	require.Empty(t, get().RequiredResponse)
	pendingItems, err = pendingFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	require.NoError(t, err)
	require.Empty(t, pendingItems.Interactions)

	// A settled input identity is remembered: it creates no new work. Until it
	// settles, the buffered input rejects a different body under its identity.
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == inputID {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", inputID, "changed-input"))
	model.mu.Lock()
	before := append([]string(nil), model.requests...)
	model.mu.Unlock()
	require.NotContains(t, strings.Join(before, "\n"), "changed-input")
	require.NotContains(t, strings.Join(before, "\n"), "forbidden-")
	// A separate AgentWork may reuse the same local message identity without
	// inheriting the first native conversation or conflicting with its input.
	otherAgent, err := client.CreateAgentDefinition(ctx, nativeRequest(&agentprivatev1.CreateAgentDefinitionRequest{Owners: []string{"bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab"}, AgentName: "Fixture Agent", Purpose: "coordination", UsageScope: &agentprivatev1.UsageScope{Kind: "project", Reference: "second-project"}, HomeBinding: &agentprivatev1.HomeBinding{OwnerId: "second-project", Namespace: "coordination"}, Capabilities: &agentprivatev1.Capabilities{}}))
	require.NoError(t, err)
	otherSession, err := client.CreateWork(ctx, nativeRequest(&agentprivatev1.CreateWorkRequest{RequestId: uuid.NewString(), AgentId: otherAgent.Msg.AgentId, Subject: &agentprivatev1.WorkSubject{Kind: "project", Id: "second-project"}, ProjectId: "second-project", SubjectId: "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab", WorkspaceReference: root}))
	require.NoError(t, err)

	records.awaitWork(t, otherSession.Msg.WorkId, command.ExecutionStarted)
	_, err = client.RespondToInteraction(ctx, nativeRequest(&agentprivatev1.RespondToInteractionRequest{WorkId: otherSession.Msg.WorkId, SubjectId: "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab", InteractionId: pending.InteractionID, Allow: true}))
	require.NoError(t, err, "another Work's interaction is not open in this Work; the reply settles as closed")

	_, err = client.DeliverInput(ctx, nativeRequest(&agentprivatev1.DeliverInputRequest{WorkId: otherSession.Msg.WorkId, SubjectId: "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab", InputId: inputID, Input: "second-conversation-only"}))
	require.NoError(t, err)
	records.awaitResult(t, "second-conversation-only")
	model.mu.Lock()
	isolated := append([]string(nil), model.requests...)
	model.mu.Unlock()
	require.Contains(t, isolated[len(isolated)-1], "second-conversation-only")
	require.NotContains(t, isolated[len(isolated)-1], "native-agent-entry-marker")
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", inputID, slashInput))
	// Real Harness task results travel through Dispatcher feedback and MySQL.
	// Both native conversations allocate task "1"; updating one must not change
	// the other's persisted plan. A fresh query process reads the same facts.
	freshDB, err := xorm.NewEngine(db.DriverName(), db.DataSourceName())
	require.NoError(t, err)
	defer freshDB.Close()
	queryApp := &agentapp.Application{Queries: agentapp.Queries{GetWorkView: agentquery.NewGetWorkViewHandler(agentinfra.NewWorkAuthorizer(freshDB), agentinfra.NewWorkQueryRepository(freshDB))}}
	_, queryHTTP := agentpublicv1connect.NewAgentWorkServiceHandler(agenttransport.NewWorkHandler(queryApp), connect.WithInterceptors(authn.NewConnectUnaryInterceptor(authn.GatewayActorConfig{Enabled: true})))
	queryServer := httptest.NewServer(queryHTTP)
	defer queryServer.Close()
	public := agentpublicv1connect.NewAgentWorkServiceClient(queryServer.Client(), queryServer.URL)
	planFor := func(workID, actor string) *agentpublicv1.WorkProgress {
		t.Helper()
		request := connect.NewRequest(&agentpublicv1.GetWorkRequest{WorkId: workID})
		request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header().Set(authn.HeaderSubject, actor)
		response, err := public.GetWork(ctx, request)
		require.NoError(t, err)
		return response.Msg.Work.WorkProgress
	}
	for _, work := range []struct{ id, actor string }{{sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8"}, {otherSession.Msg.WorkId, "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab"}} {
		model.mu.Lock()
		model.taskNext = "TaskCreate"
		model.mu.Unlock()
		_, err = client.DeliverInput(ctx, nativeRequest(&agentprivatev1.DeliverInputRequest{WorkId: work.id, SubjectId: work.actor, InputId: uuid.NewString(), Input: "Create the Work plan"}))
		require.NoError(t, err)
		require.Eventually(t, func() bool {
			progress := planFor(work.id, work.actor)
			return progress != nil && len(progress.Items) == 1 && progress.Items[0].Status == "pending"
		}, 10*time.Second, 20*time.Millisecond)
	}
	model.mu.Lock()
	model.taskNext = "TaskUpdate"
	model.mu.Unlock()
	progressInputID := uuid.NewString()
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", progressInputID, "Complete native task 1"))
	require.Eventually(t, func() bool {
		progress := planFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
		return progress != nil && progress.Items[0].Status == "completed"
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, "pending", planFor(otherSession.Msg.WorkId, "bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab").Items[0].Status)
	require.Equal(t, "Verify native Work progress", planFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8").Items[0].Content)
	// Finish the preceding Progress turn before controlling the input/stop race.
	// Progress is observable before its turn-result and input-completion writes.
	records.awaitResult(t, "Complete native task 1")
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == progressInputID {
				return true
			}
		}
		return false
	}, 10*time.Second, 10*time.Millisecond)

	// Public cancellation uses the same authorization and current Work identity,
	// but does not enter Work's stop lifecycle or release its compute.
	model.mu.Lock()
	model.questionNext = true
	model.mu.Unlock()
	cancelInputID := uuid.NewString()
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", cancelInputID, "cancel through public Work API"))
	require.Eventually(t, func() bool {
		items, readErr := pendingFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
		return readErr == nil && len(items.Interactions) == 1
	}, 5*time.Second, 20*time.Millisecond)
	currentFor := func(actor string) (*agentpublicv1.GetCurrentTurnResponse, error) {
		request := connect.NewRequest(&agentpublicv1.GetCurrentTurnRequest{WorkId: sessionID})
		request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header().Set(authn.HeaderSubject, actor)
		response, err := interactionClient.GetCurrentTurn(ctx, request)
		if err != nil {
			return nil, err
		}
		return response.Msg, nil
	}
	cancelFor := func(actor, turnID string) error {
		request := connect.NewRequest(&agentpublicv1.CancelTurnRequest{WorkId: sessionID, TurnId: turnID})
		request.Header().Set(authn.HeaderAuthenticatedBy, "envoy")
		request.Header().Set(authn.HeaderSubject, actor)
		_, err := interactionClient.CancelTurn(ctx, request)
		return err
	}
	current, err := currentFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	require.NoError(t, err)
	require.NotEmpty(t, current.TurnId)
	_, err = currentFor("bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab")
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(cancelFor("bced4b5e-5ad5-5cd8-9b07-c93b2c7745ab", current.TurnId)))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(cancelFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8", "")))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(cancelFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8", "previous-turn")))
	require.NoError(t, cancelFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8", current.TurnId))
	records.awaitResult(t, "cancel through public Work API")
	require.Eventually(t, func() bool {
		view := get()
		return view.State == agentprivatev1.WorkState_WORK_STATE_WORKING && !view.Waiting && !view.RequiredResponse
	}, 5*time.Second, 20*time.Millisecond)
	current, err = currentFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	require.NoError(t, err)
	require.Empty(t, current.TurnId)
	// Wait for the native completion receipt before exercising the independent
	// input-versus-Work-stop ordering below.
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == cancelInputID {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond)
	assertNativeErrorAllowsNextInput(t, model, records, get, func(text string) error {
		return send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", uuid.NewString(), text)
	})
	// A Work stops only between Turns: a stop during a Turn is refused, and
	// stopping or closing proceeds once the Turn is cancelled.
	model.mu.Lock()
	model.questionNext = true
	model.mu.Unlock()
	holdInputID := uuid.NewString()
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", holdInputID, "hold the turn open"))
	require.Eventually(t, func() bool {
		items, readErr := pendingFor(sessionID, "e6c7e756-007d-5236-a492-7f4b9be0b2e8")
		work, workErr := sessions.Get(ctx, sessionID)
		observed, observeErr := manager.ObserveProcess(ctx, sessionID, resources.process(sessionID))
		return readErr == nil && len(items.Interactions) == 1 && workErr == nil && work.HarnessProcess != nil && work.HarnessProcess.Turn.Active && observeErr == nil && work.HarnessProcess.Turn.Sequence == observed.Turn.Sequence
	}, 5*time.Second, 20*time.Millisecond, "Agent must apply the running Turn before its stop admission is exercised")
	_, err = client.StopWork(ctx, nativeRequest(&agentprivatev1.StopWorkRequest{WorkId: sessionID, SubjectId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", RequestId: "stop-request"}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a stop during a Turn is refused: %v", err)
	require.Contains(t, err.Error(), "cancel it before stopping the Work")
	current, err = currentFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8")
	require.NoError(t, err)
	require.NoError(t, cancelFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8", current.TurnId))
	records.awaitResult(t, "hold the turn open")
	require.Eventually(t, func() bool {
		turn, readErr := currentFor("e6c7e756-007d-5236-a492-7f4b9be0b2e8")
		work, workErr := sessions.Get(ctx, sessionID)
		if readErr != nil || turn.TurnId != "" || workErr != nil || work.HarnessProcess == nil || work.HarnessProcess.Turn.Active {
			return false
		}
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == holdInputID {
				return true
			}
		}
		return false
	}, 5*time.Second, 20*time.Millisecond, "Agent must finish applying cancellation feedback before Work control")
	model.mu.Lock()
	requestsBeforeStop := len(model.requests)
	model.mu.Unlock()
	_, err = client.StopWork(ctx, nativeRequest(&agentprivatev1.StopWorkRequest{WorkId: sessionID, SubjectId: "e6c7e756-007d-5236-a492-7f4b9be0b2e8", RequestId: "stop-request"}))
	require.NoError(t, err)
	records.awaitWork(t, sessionID, command.ExecutionExited)
	// The controlled provisioner confirms removal at the same application
	// boundary as the real Reclaimer; Agent still owns the stop transition.
	require.NoError(t, app.Commands.ApplyExecutionFeedback.Handle(ctx, agentcommand.ExecutionFeedback{WorkID: sessionID, Kind: agentcommand.ComputeReclaimed}))
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, get().State)
	// Stopped Works accept input for a later Resume without starting a process.
	stoppedInputID := uuid.NewString()
	require.NoError(t, send("e6c7e756-007d-5236-a492-7f4b9be0b2e8", stoppedInputID, "buffer-for-resume"))
	queued, err := buffer.List(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, queued, 1)
	require.Equal(t, stoppedInputID, queued[0].ID)
	require.Equal(t, "buffer-for-resume", queued[0].Text)
	require.Equal(t, agentprivatev1.WorkState_WORK_STATE_STOPPED, get().State)
	model.mu.Lock()
	after := append([]string(nil), model.requests...)
	model.mu.Unlock()
	require.Len(t, after, requestsBeforeStop)
	require.NotContains(t, strings.Join(after, "\n"), "forbidden-")
}

// grantedMounts stands in for Storage: each start gets its Work's spaces.
type grantedMounts struct{}

func (grantedMounts) AcquireMounts(_ context.Context, request agentdomain.SandboxMountRequest) (agentdomain.SandboxMounts, error) {
	return agentdomain.SandboxMounts{Workspace: &agentdomain.MountGrant{ClaimName: "nas", SubPath: "workspaces/" + request.WorkspaceReference}, Home: agentdomain.MountGrant{ClaimName: "nas", SubPath: "homes/" + request.HomeOwner}}, nil
}

// cloudOnly places every Work on a Cloud.
type cloudOnly struct{}

func (cloudOnly) DescribeComputer(context.Context, string) (agentcommand.ComputerDescription, error) {
	return agentcommand.ComputerDescription{}, nil
}

//go:build integration

package dispatcher_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	dispatcherprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/private/v1"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"google.golang.org/protobuf/proto"
)

// This tests the actual private HTTP API, Dispatcher loop, execd and Claude.
// Inference is deterministic; feedback and message storage are recording
// seams. Ownership and snapshots use real Redis; K8s recovery is separate evidence.
func TestNativeExecutionThroughPrivateAPI(t *testing.T) {
	f := newNativeExecutionFixture(t)
	var err error
	ref := f.start("first", false)
	_, err = f.client.SetThinkingLevel(f.ctx, connect.NewRequest(&dispatcherprivatev1.SetThinkingLevelRequest{WorkId: nativePrivateWork, Level: "low"}))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = f.client.SetThinkingLevel(f.ctx, nativeRequest(&dispatcherprivatev1.SetThinkingLevelRequest{WorkId: nativePrivateWork, Level: "bogus"}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	_, err = f.client.SetThinkingLevel(f.ctx, nativeRequest(&dispatcherprivatev1.SetThinkingLevelRequest{WorkId: "unrelated-work", Level: "low"}))
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = f.client.SetThinkingLevel(f.ctx, nativeRequest(&dispatcherprivatev1.SetThinkingLevelRequest{WorkId: nativePrivateWork, Level: "low"}))
	require.NoError(t, err)
	level, err := f.client.GetThinkingLevel(f.ctx, nativeRequest(&dispatcherprivatev1.GetThinkingLevelRequest{WorkId: nativePrivateWork}))
	require.NoError(t, err)
	require.Equal(t, "low", level.Msg.Level)
	// The Harness switches its permission mode at once and answers the mode
	// it accepted, which reaches Agent as this process's report; a mode the
	// Harness does not know does not reach it.
	_, err = f.client.SetPermissionMode(f.ctx, nativeRequest(&dispatcherprivatev1.SetPermissionModeRequest{WorkId: nativePrivateWork, Mode: "bogus"}))
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	accepted, err := f.client.SetPermissionMode(f.ctx, nativeRequest(&dispatcherprivatev1.SetPermissionModeRequest{WorkId: nativePrivateWork, Mode: "plan"}))
	require.NoError(t, err)
	require.Equal(t, "plan", accepted.Msg.GetMode())
	require.Eventually(t, func() bool {
		f.records.mu.Lock()
		defer f.records.mu.Unlock()
		for _, o := range f.records.observations[f.records.runs["first"]:] {
			if o.Kind == command.ExecutionPermissionModeObserved && o.PermissionMode == "plan" && o.ProcessID == f.records.processes["first"] {
				return true
			}
		}
		return false
	}, 15*time.Second, 10*time.Millisecond, "the accepted mode is reported as the process's")
	accepted, err = f.client.SetPermissionMode(f.ctx, nativeRequest(&dispatcherprivatev1.SetPermissionModeRequest{WorkId: nativePrivateWork, Mode: "bypassPermissions"}))
	require.NoError(t, err)
	require.Equal(t, "bypassPermissions", accepted.Msg.GetMode())
	f.records.mu.Lock()
	f.records.liveUnavailable = true
	f.records.rejectInputCompletion = true
	f.records.mu.Unlock()
	input := f.send(ref, "remember native-dispatcher-marker and ask permission")
	pending := f.records.await(t, "first", command.ExecutionUserInteractionRequired)
	require.NotEmpty(t, pending.InteractionID)
	pendingQuery := &dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: nativePrivateWork}
	_, err = f.client.ListPendingInteractions(f.ctx, connect.NewRequest(pendingQuery))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	pendingList, err := f.client.ListPendingInteractions(f.ctx, nativeRequest(pendingQuery))
	require.NoError(t, err)
	require.Len(t, pendingList.Msg.Interactions, 1)
	question := pendingList.Msg.Interactions[0]
	require.Equal(t, pending.InteractionID, question.Id)
	require.Equal(t, "AskUserQuestion", question.ToolName)
	require.NotEmpty(t, question.TurnId)
	require.NotNil(t, question.Agent)
	require.Empty(t, *question.Agent)
	require.Contains(t, question.QuestionInputJson, "Proceed?")
	require.False(t, question.ReplySubmitted)

	// Destroy every in-memory runtime object while native Claude is waiting on
	// AskUserQuestion. The new manager must resume the saved stream and pending
	// control request; it cannot reinitialize a new native process.
	require.NoError(t, f.manager.Shutdown(f.ctx))
	f.manager, f.client = f.runtime()
	_, err = f.client.Connect(f.ctx, nativeRequest(f.targets["first"]))
	require.NoError(t, err)
	// Connect admits reconnection before execd acknowledges the attachment;
	// until it is replayed, the stream is not observed and reads of it answer
	// Unavailable.
	var recoveredPending *connect.Response[dispatcherprivatev1.ListPendingInteractionsResponse]
	require.Eventually(t, func() bool {
		recoveredPending, err = f.client.ListPendingInteractions(f.ctx, nativeRequest(pendingQuery))
		if err != nil && connect.CodeOf(err) != connect.CodeUnavailable {
			require.NoError(t, err)
		}
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	require.Len(t, recoveredPending.Msg.Interactions, 1)
	// MySQL JSON checkpointing normalizes object key order and whitespace;
	// question content and all routing identities must remain identical.
	require.JSONEq(t, question.QuestionInputJson, recoveredPending.Msg.Interactions[0].QuestionInputJson)
	normalizedPending := proto.Clone(recoveredPending.Msg).(*dispatcherprivatev1.ListPendingInteractionsResponse)
	normalizedPending.Interactions[0].QuestionInputJson = question.QuestionInputJson
	require.True(t, proto.Equal(pendingList.Msg, normalizedPending), "native pending state survives process-manager replacement without message history")
	// Hand-off retries are safe throughout reconnection and must not create a
	// second native turn; the reply below also exercises recovered Pending.
	handOff, err := f.enqueue(&dispatcherprivatev1.EnqueueInputRequest{Entry: &dispatcherprivatev1.EnqueueInputRequest_Text{Text: input}})
	require.NoError(t, err)
	require.Equal(t, dispatcherprivatev1.InputHandOff_INPUT_HAND_OFF_REPEATED, handOff)
	changed := proto.Clone(input).(*dispatcherprivatev1.BufferedText)
	changed.Text = "different"
	_, err = f.enqueue(&dispatcherprivatev1.EnqueueInputRequest{Entry: &dispatcherprivatev1.EnqueueInputRequest_Text{Text: changed}})
	require.Equal(t, connect.CodeAlreadyExists, connect.CodeOf(err))
	answer := &dispatcherprivatev1.BufferedReply{InteractionId: pending.InteractionID, Allow: true, UpdatedInputJson: `{"questions":[{"question":"Proceed?","header":"Choice","options":[{"label":"Yes","description":"Proceed"},{"label":"No","description":"Stop"}],"multiSelect":false}],"answers":{"Proceed?":"Yes"}}`}
	f.respond(answer)
	f.respond(answer)
	resolved := f.records.await(t, "first", command.ExecutionUserInteractionResolved)
	require.Equal(t, pending.InteractionID, resolved.InteractionID)
	pendingList, err = f.client.ListPendingInteractions(f.ctx, nativeRequest(pendingQuery))
	require.NoError(t, err)
	require.Empty(t, pendingList.Msg.Interactions, "native acknowledgement removes the pending request")

	f.records.awaitResult(t, "remember native-dispatcher-marker and ask permission")
	turnStarted := f.records.await(t, "first", command.ExecutionTurnStarted)
	turnFinished := f.records.await(t, "first", command.ExecutionTurnFinished)
	require.Positive(t, turnStarted.ObservationSequence)
	require.Greater(t, turnFinished.ObservationSequence, turnStarted.ObservationSequence)
	require.True(t, turnFinished.TurnCompleted, "normal result is distinct from process exit")
	// The Turn's progress is reported ahead of its end, at most once a minute
	// by each owner however much output it produced; this Turn's stream was
	// handed to a new owner above, which reports again.
	activity := f.records.await(t, "first", command.ExecutionTurnActivity)
	require.Less(t, activity.ObservationSequence, turnFinished.ObservationSequence)
	f.records.mu.Lock()
	reports := 0
	for _, o := range f.records.observations[f.records.runs["first"]:] {
		if o.Kind == command.ExecutionTurnActivity {
			reports++
		}
	}
	f.records.mu.Unlock()
	require.Equal(t, 2, reports, "one report from each owner within the minute")
	require.Eventually(t, func() bool { f.records.mu.Lock(); defer f.records.mu.Unlock(); return f.records.lostInputCompletion }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, f.manager.Shutdown(f.ctx))
	f.records.mu.Lock()
	f.records.rejectInputCompletion = false
	f.records.mu.Unlock()
	f.manager, f.client = f.runtime()
	_, err = f.client.Connect(f.ctx, nativeRequest(f.targets["first"]))
	require.NoError(t, err)
	currentInput, err := f.client.ObserveProcess(f.ctx, nativeRequest(&dispatcherprivatev1.ObserveProcessRequest{WorkId: nativePrivateWork, ProcessId: f.targets["first"].ProcessId}))
	require.NoError(t, err)
	require.True(t, currentInput.Msg.GetTurn().GetCompleted())
	buffered, err := f.client.ListBufferedInput(f.ctx, nativeRequest(&dispatcherprivatev1.ListBufferedInputRequest{WorkId: nativePrivateWork}))
	require.NoError(t, err)
	require.Empty(t, buffered.Msg.InputIds, "the buffer recovers lost settlement feedback without replaying it")
	f.records.assertQuestion(t, pending.InteractionID, dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_RESPONDED, `{"Proceed?":"Yes"}`)
	f.model.mu.Lock()
	answeredRequests := strings.Join(f.model.requests, "\n")
	f.model.mu.Unlock()
	require.Contains(t, answeredRequests, `Yes`)
	require.Contains(t, answeredRequests, `"effort":"low"`, "Harness settings must reach the model request")
	require.Eventually(t, func() bool {
		_, err = f.client.SetThinkingLevel(f.ctx, nativeRequest(&dispatcherprivatev1.SetThinkingLevelRequest{WorkId: nativePrivateWork, Level: "high"}))
		return err == nil
	}, 5*time.Second, 20*time.Millisecond)
	f.send(ref, "verify adjusted effort on the same conversation")
	require.Eventually(t, func() bool {
		f.model.mu.Lock()
		defer f.model.mu.Unlock()
		return strings.Contains(f.model.requests[len(f.model.requests)-1], `"effort":"high"`)
	}, 5*time.Second, 20*time.Millisecond)

	f.model.mu.Lock()
	f.model.taskNext = "TaskCreate"
	f.model.mu.Unlock()
	f.records.mu.Lock()
	f.records.rejectPlan = true
	f.records.mu.Unlock()
	f.send(ref, "create the Work plan")
	f.records.awaitResult(t, "create the Work plan")
	require.Eventually(t, func() bool { f.records.mu.Lock(); defer f.records.mu.Unlock(); return f.records.lostPlan }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, f.manager.Shutdown(f.ctx))
	f.records.mu.Lock()
	f.records.rejectPlan = false
	f.records.mu.Unlock()
	f.manager, f.client = f.runtime()
	_, err = f.client.Connect(f.ctx, nativeRequest(f.targets["first"]))
	require.NoError(t, err)
	// The missed projection is deliberately not retried. The resumed TaskUpdate
	// below must still use the retained native task state.

	// Lose exit feedback, then remove the ended provider handle. The next runtime
	// redelivers the exit fact from the snapshot without the process.
	f.records.mu.Lock()
	f.records.rejectExit = true
	f.records.mu.Unlock()
	// The reconnected execution accepts the Stop once it is ready again.
	stopID := uuid.NewString()
	require.Eventually(t, func() bool {
		_, err = f.client.Stop(f.ctx, nativeRequest(&dispatcherprivatev1.StopRequest{WorkId: nativePrivateWork, StopRequestId: stopID}))
		return connect.CodeOf(err) != connect.CodeFailedPrecondition
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		f.records.mu.Lock()
		defer f.records.mu.Unlock()
		return f.records.lostExit
	}, 5*time.Second, 20*time.Millisecond)
	require.NoError(t, f.manager.Shutdown(f.ctx))
	require.NoError(t, f.processClient.Delete(f.ctx, f.targets["first"].ProcessId))
	f.records.mu.Lock()
	f.records.rejectExit = false
	f.records.mu.Unlock()
	f.records.mark("first-redelivered", f.targets["first"].ProcessId)
	f.manager, f.client = f.runtime()
	_, err = f.client.Connect(f.ctx, nativeRequest(f.targets["first"]))
	require.NoError(t, err)
	redelivered := f.records.await(t, "first-redelivered", command.ExecutionExited)
	require.Equal(t, f.targets["first"].ProcessId, redelivered.ProcessID, "the unacknowledged exit survives the deleted process handle")
	resumed := f.start("resumed", true)
	f.model.mu.Lock()
	f.model.taskNext = "TaskUpdate"
	f.model.mu.Unlock()
	f.send(resumed, "continue saved history")
	resumedPlan := f.records.await(t, "resumed", command.ExecutionWorkProgressUpdated)
	require.Equal(t, []command.WorkProgressItem{{Content: "Verify native Work progress", Status: "completed", ActiveForm: "Verifying native Work progress"}}, resumedPlan.WorkProgress)
	f.records.awaitResult(t, "continue saved history")
	f.stop(resumed)
	// A Work stops only between Turns: a stop is refused while a question holds
	// the Turn open; cancelling the Turn resolves the question and preserves
	// history, and the stop then proceeds.
	cancelCurrentTurn := func() {
		t.Helper()
		turn, err := f.client.GetCurrentTurn(f.ctx, nativeRequest(&dispatcherprivatev1.GetCurrentTurnRequest{WorkId: nativePrivateWork}))
		require.NoError(t, err)
		require.NotEmpty(t, turn.Msg.TurnId)
		_, err = f.client.CancelTurn(f.ctx, nativeRequest(&dispatcherprivatev1.CancelTurnRequest{WorkId: nativePrivateWork, TurnId: turn.Msg.TurnId}))
		require.NoError(t, err)
	}
	f.model.mu.Lock()
	f.model.questionNext = true
	f.model.mu.Unlock()
	third := f.start("interrupted", true)
	f.send(third, "interrupt-marker")
	cancelled := f.records.await(t, "interrupted", command.ExecutionUserInteractionRequired)
	_, err = f.client.Stop(f.ctx, nativeRequest(&dispatcherprivatev1.StopRequest{WorkId: nativePrivateWork, StopRequestId: uuid.NewString()}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a stop during a Turn is refused")
	cancelCurrentTurn()
	f.records.await(t, "interrupted", command.ExecutionUserInteractionResolved)
	f.records.awaitResult(t, "interrupt-marker")
	f.stop(third)
	f.records.assertQuestion(t, cancelled.InteractionID, dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_CANCELLED, "")
	fourth := f.start("after-interrupt", true)
	f.send(fourth, "continue after interruption")
	f.records.awaitResult(t, "continue after interruption")
	f.stop(fourth)
	f.model.mu.Lock()
	f.model.questionNext = true
	f.model.mu.Unlock()
	denied := f.start("denied", true)
	f.send(denied, "deny this question")
	deniedRequest := f.records.await(t, "denied", command.ExecutionUserInteractionRequired)
	f.respond(&dispatcherprivatev1.BufferedReply{InteractionId: deniedRequest.InteractionID, Allow: false, Reason: "Not this time"})
	f.records.awaitResult(t, "deny this question")
	f.records.assertQuestion(t, deniedRequest.InteractionID, dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_SKIPPED, "")
	f.stop(denied)

	// Cancel only the active turn: no exit/release and the same native process
	// must accept a following question and ordinary input.
	f.model.mu.Lock()
	f.model.questionNext = true
	f.model.mu.Unlock()
	cancelRef := f.start("cancel-turn", true)
	f.send(cancelRef, "cancel this question")
	cancelQuestion := f.records.await(t, "cancel-turn", command.ExecutionUserInteractionRequired)
	turnQuery := &dispatcherprivatev1.GetCurrentTurnRequest{WorkId: nativePrivateWork}
	_, err = f.client.GetCurrentTurn(f.ctx, connect.NewRequest(turnQuery))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	activeTurn, err := f.client.GetCurrentTurn(f.ctx, nativeRequest(turnQuery))
	require.NoError(t, err)
	require.NotEmpty(t, activeTurn.Msg.TurnId)
	require.True(t, activeTurn.Msg.Active)
	cancelRequest := &dispatcherprivatev1.CancelTurnRequest{WorkId: nativePrivateWork, TurnId: activeTurn.Msg.TurnId}
	_, err = f.client.CancelTurn(f.ctx, connect.NewRequest(cancelRequest))
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	_, err = f.client.CancelTurn(f.ctx, nativeRequest(&dispatcherprivatev1.CancelTurnRequest{WorkId: nativePrivateWork, TurnId: "older-turn"}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
	_, err = f.client.CancelTurn(f.ctx, nativeRequest(cancelRequest))
	require.NoError(t, err)
	// Reconnection must retain cancellation without ending the Work process.
	require.NoError(t, f.manager.Shutdown(f.ctx))
	f.manager, f.client = f.runtime()
	_, err = f.client.Connect(f.ctx, nativeRequest(f.targets[cancelRef]))
	require.NoError(t, err)
	// How the Harness ends an interrupted Turn is its own behavior; the Turn
	// ending is all this flow needs before the next input. Its pace is the
	// Harness's, which a loaded machine slows, so only the ending is bounded.
	f.records.awaitResult(t, "cancel this question")
	require.Eventually(t, func() bool {
		idle, readErr := f.client.GetCurrentTurn(f.ctx, nativeRequest(turnQuery))
		return readErr == nil && !idle.Msg.Active
	}, 30*time.Second, 20*time.Millisecond)
	f.model.mu.Lock()
	f.model.questionNext = true
	f.model.mu.Unlock()
	f.send(cancelRef, "a later question remains active")
	require.Eventually(t, func() bool {
		later, readErr := f.client.GetCurrentTurn(f.ctx, nativeRequest(turnQuery))
		return readErr == nil && later.Msg.TurnId != "" && later.Msg.TurnId != activeTurn.Msg.TurnId
	}, 5*time.Second, 20*time.Millisecond)
	// The same request after the next turn starts must not interrupt that turn.
	_, err = f.client.CancelTurn(f.ctx, nativeRequest(cancelRequest))
	require.NoError(t, err)
	var laterPending *connect.Response[dispatcherprivatev1.ListPendingInteractionsResponse]
	require.Eventually(t, func() bool {
		laterPending, err = f.client.ListPendingInteractions(f.ctx, nativeRequest(&dispatcherprivatev1.ListPendingInteractionsRequest{WorkId: nativePrivateWork}))
		return err == nil && len(laterPending.Msg.Interactions) == 1
	}, 5*time.Second, 20*time.Millisecond)
	later := laterPending.Msg.Interactions[0]
	require.NotEqual(t, cancelQuestion.InteractionID, later.Id)
	f.respond(&dispatcherprivatev1.BufferedReply{InteractionId: later.Id, Allow: false, Reason: "Skip"})
	f.records.awaitResult(t, "a later question remains active")
	f.send(cancelRef, "continue in the same running Work")
	f.records.awaitResult(t, "continue in the same running Work")
	f.records.mu.Lock()
	for _, observation := range f.records.observations[f.records.runs[cancelRef]:] {
		require.NotEqual(t, command.ExecutionExited, observation.Kind)
	}
	f.records.mu.Unlock()
	f.stop(cancelRef)
	// Wait until the actual native Bash tool has started: the stop waits while
	// the command runs, and cancelling the Turn interrupts the command.
	toolMarker := filepath.Join(f.root, "running-tool")
	f.model.mu.Lock()
	f.model.bashNext = "touch '" + toolMarker + "'; sleep 60"
	f.model.mu.Unlock()
	toolExecution := f.start("running-tool-stop", true)
	f.send(toolExecution, "run the long command")
	require.Eventually(t, func() bool { _, err := os.Stat(toolMarker); return err == nil }, 5*time.Second, 20*time.Millisecond)
	_, err = f.client.Stop(f.ctx, nativeRequest(&dispatcherprivatev1.StopRequest{WorkId: nativePrivateWork, StopRequestId: uuid.NewString()}))
	require.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err), "a running tool keeps the Turn active")
	cancelCurrentTurn()
	f.records.awaitResult(t, "run the long command")
	f.stop(toolExecution)
	toolTarget := f.targets[toolExecution]
	toolExit := f.records.await(t, toolExecution, command.ExecutionExited)
	require.Equal(t, toolTarget.ProcessId, toolExit.ProcessID)
	require.Zero(t, toolExit.ExitCode)
	require.Empty(t, toolExit.ExitReason, "the interrupted turn ended before the stop signal")
	early := f.start("early-stop", true)
	f.send(early, "stop immediately after input delivery")
	f.stop(early)
	f.model.mu.Lock()
	f.model.taskNext = "Agent"
	f.model.mu.Unlock()
	delegated := f.start("delegation", true)
	f.send(delegated, "delegate a short independent check")
	f.records.awaitResult(t, "delegate a short independent check")
	f.records.mu.Lock()
	var childMessages int
	var childAgent, childTurn string
	for _, message := range f.records.messages {
		if message.Agent != nil && *message.Agent != "" {
			childMessages++
			childAgent, childTurn = *message.Agent, message.TurnId
			require.NotEmpty(t, message.TurnId)
		}
	}
	f.records.mu.Unlock()
	require.Positive(t, childMessages, "native child messages must retain the actual subagent identity")
	f.model.mu.Lock()
	f.model.taskNext, f.model.taskTarget = "SendMessage", childAgent
	f.model.mu.Unlock()
	f.send(delegated, "ask the same member for a follow-up")
	require.Eventually(t, func() bool {
		f.records.mu.Lock()
		defer f.records.mu.Unlock()
		for _, message := range f.records.messages {
			if message.GetAgent() == childAgent && message.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ASSISTANT && message.TurnId != "" && message.TurnId != childTurn {
				return true
			}
		}
		return false
	}, 8*time.Second, 20*time.Millisecond, "native member resumption must retain Agent identity in a new member turn")
	f.records.awaitResult(t, "ask the same member for a follow-up")
	f.stop(delegated)
	// A real API rejection that needs a person reaches product history only
	// through the Turn's failed Result, which keeps the provider's original
	// explanation; the native API error frame itself is not history.
	f.model.mu.Lock()
	f.model.apiFailure = true
	f.model.mu.Unlock()
	rejectedAPI := f.start("api-rejection", true)
	f.send(rejectedAPI, "exercise authentication failure")
	f.records.awaitResult(t, "exercise authentication failure")
	func() {
		f.records.mu.Lock()
		defer f.records.mu.Unlock()
		explained := false
		for _, message := range f.records.messages {
			require.NotEqual(t, "authentication_failed", message.GetError().GetCode(), "the native API error frame entered history")
			if result := message.GetResult(); result != nil && result.Status == dispatcherpublicv1.ResultStatus_RESULT_STATUS_FAILED && strings.Contains(result.Text, "fixture API credential rejected") {
				require.NotNil(t, message.Agent)
				require.Empty(t, *message.Agent)
				explained = true
			}
		}
		require.True(t, explained, "the failed Result must carry the provider's explanation")
	}()
	f.model.mu.Lock()
	f.model.apiFailure = false
	f.model.mu.Unlock()
	f.stop(rejectedAPI)
	// A real nonzero process exit must remain a failure while its exit fact still
	// permits resource cleanup. It cannot produce native readiness or a normal stop.
	failedHandle, err := f.processClient.Create(f.ctx, f.root, "exit 23")
	require.NoError(t, err)
	failedRef := "failed-process"
	f.records.mark(failedRef, failedHandle)
	failedConnect := &dispatcherprivatev1.ConnectRequest{WorkId: nativePrivateWork, Endpoint: f.endpoint, AccessToken: "execd-test", ProcessId: failedHandle, NativeSessionId: uuid.NewString()}
	f.connect(failedConnect)
	// The exit fact carries the code; Agent classifies it.
	failure := f.records.awaitExited(t, failedRef, nativePrivateWork, func() { f.connect(failedConnect) })
	require.Equal(t, failedHandle, failure.ProcessID)
	require.Equal(t, 23, failure.ExitCode)
	require.Empty(t, failure.ExitReason)
	f.records.mu.Lock()
	for _, item := range f.records.messages {
		require.Nil(t, item.GetError(), "an unexpected exit is the Work's fact, not a conversation message")
	}
	for _, observation := range f.records.observations[f.records.runs[failedRef]:] {
		require.NotEqual(t, command.ExecutionStarted, observation.Kind, "a failed process never becomes ready")
	}
	defer f.records.mu.Unlock()
	require.NotEmpty(t, f.records.messages)
	for _, message := range f.records.messages {
		require.Equal(t, "session-test", message.AgentWorkId)
		require.NotContains(t, message.String(), "settings-only-secret", "native settings must never enter public conversation history")
	}
}

//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/messagedelivery"
	"golang.org/x/net/websocket"
)

// Intent (real): a replica handing the stream over while a stored RESULT is
// still being published live resumes at the exact position; the peer neither
// repeats the RESULT nor its turn feedback.
func TestHandoverResumesAfterAStoredResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	transcript := "{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"initialize-process\"}}\n" +
		"{\"type\":\"assistant\",\"uuid\":\"root\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"Done\"}]}}\n" +
		"{\"type\":\"result\",\"subtype\":\"success\",\"result\":\"Done\"}\n"
	var initialized atomic.Bool
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		connected := `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`
		if !initialized.Swap(true) {
			if websocket.Message.Send(ws, connected) != nil {
				return
			}
			var request []byte
			if websocket.Message.Receive(ws, &request) != nil {
				return
			}
			if websocket.Message.Send(ws, append([]byte{1}, transcript...)) != nil {
				return
			}
		} else {
			offset, err := strconv.ParseInt(ws.Request().URL.Query().Get("since"), 10, 64)
			if err != nil || offset < 0 || offset > int64(len(transcript)) {
				return
			}
			replay := make([]byte, 9)
			replay[0] = 3
			binary.BigEndian.PutUint64(replay[1:], uint64(offset))
			if websocket.Message.Send(ws, append(replay, transcript[offset:]...)) != nil || websocket.Message.Send(ws, connected) != nil {
				return
			}
		}
		var request []byte
		_ = websocket.Message.Receive(ws, &request)
	}))
	defer server.Close()
	records := &nativeRecords{}
	replicas := newNativeReplicas(t)
	stopped := make(chan error, 1)
	var first *dispatcher.NativeExecutions
	live := &interruptResultDelivery{stop: func() {
		shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		stopped <- first.Shutdown(shutdown)
	}}
	managers := 0
	newManager := func(publisher messagedelivery.LivePublisher) *dispatcher.NativeExecutions {
		managers++
		return replicas.replica(fmt.Sprintf("replica-%d", managers), records, newTestDelivery(t, records, publisher))
	}
	first = newManager(live)
	target := command.ConnectExecution{WorkID: "work-replay", Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, first.Connect(ctx, target))
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("Result delivery did not trigger shutdown")
	}
	require.NoError(t, newManager(records).Connect(ctx, target))
	var results []*dispatcherpublicv1.UnifiedMessage
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		results = nil
		for _, message := range records.messages {
			if message.GetResult() != nil {
				results = append(results, message)
			}
		}
		return len(results) == 1
	}, 5*time.Second, 10*time.Millisecond)
	time.Sleep(300 * time.Millisecond) // past a batch interval: nothing more arrives
	records.mu.Lock()
	defer records.mu.Unlock()
	finishedReports, stored := 0, 0
	for _, observation := range records.observations {
		if observation.Kind == command.ExecutionTurnFinished {
			finishedReports++
		}
	}
	for _, message := range records.messages {
		if message.GetResult() != nil {
			stored++
		}
		require.Nil(t, message.GetMessage(), "the identical final assistant must never reach storage")
	}
	require.Equal(t, 1, finishedReports, "the peer does not repeat consumed turn feedback")
	require.Equal(t, 1, stored, "the peer does not repeat the RESULT")
	require.NotEmpty(t, results[0].TurnId)
}

type interruptResultDelivery struct{ stop func() }

// PublishLive starts the handover while the RESULT is being published and
// fails that publication once the replica has handed over.
func (p *interruptResultDelivery) PublishLive(ctx context.Context, message *dispatcherpublicv1.UnifiedMessage) error {
	if message.GetResult() == nil {
		return nil
	}
	handedOver := make(chan struct{})
	go func() { p.stop(); close(handedOver) }()
	select {
	case <-handedOver:
		return errors.New("replica handed the stream over")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A native control/protocol failure is retained with the Harness's explanation
// and reaches Agent with the exit fact; it is not a conversation message.
// Reconnecting the same failed stream resumes from its snapshot.
func TestHarnessFailureIsRetainedOutsideHistory(t *testing.T) {
	replicas := newNativeReplicas(t)
	for _, tc := range []struct {
		name, transcript, failure string
	}{
		{"initialize", `{"type":"control_response","response":{"subtype":"error","request_id":"initialize-failure","error":"Provider configuration unavailable"}}` + "\n", "native_initialization_failed: Provider configuration unavailable"},
		{"disconnect", "", ""},
		{"unexpected_stderr", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
				defer ws.Close()
				if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
					return
				}
				var request []byte
				if tc.name == "unexpected_stderr" {
					_ = websocket.Message.Send(ws, []byte{2, 'e', 'r', 'r'})
					_ = websocket.Message.Receive(ws, &request)
					return
				}
				if websocket.Message.Receive(ws, &request) == nil && tc.transcript != "" {
					transcript := strings.ReplaceAll(tc.transcript, "initialize-failure", "initialize-process")
					_ = websocket.Message.Send(ws, append([]byte{1}, transcript...))
					_ = websocket.Message.Receive(ws, &request)
				}
			}))
			defer server.Close()
			records := &nativeRecords{}
			var deliveries []*messagedelivery.Sink
			target := command.ConnectExecution{WorkID: "work-" + tc.name, Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
			for attempt := 0; attempt < 2; attempt++ {
				delivery := newTestDelivery(t, records, records)
				deliveries = append(deliveries, delivery)
				manager := replicas.replica(fmt.Sprintf("%s-%d", tc.name, attempt), records, delivery)
				require.NoError(t, manager.Connect(ctx, target))
				require.Eventually(t, func() bool {
					_, err := manager.GetCurrentTurn(ctx, target.WorkID)
					return errors.Is(err, command.ErrNoActiveConnection)
				}, 3*time.Second, 10*time.Millisecond)
				require.NoError(t, manager.Shutdown(ctx))
			}
			// Closing a Sink appends what it still holds.
			for _, delivery := range deliveries {
				require.NoError(t, delivery.Close(ctx))
			}
			records.mu.Lock()
			defer records.mu.Unlock()
			require.Empty(t, records.messages, "a process failure is not a conversation message")
			if tc.failure == "" {
				require.Empty(t, records.observations, "disconnect does not assert a failed turn or Work")
				return
			}
			require.Empty(t, records.observations, "the failure reaches Agent with the exit fact, which this process has not produced")
			stored := replicas.stored(target)
			require.Equal(t, tc.failure, stored.Failure)
		})
	}
}

// A written input is already busy even before Claude assigns its turn, and it
// stays buffered until its own terminal disposition settles it.
func TestAcceptedInputBeforeTurnIdentity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	frames := make(chan string)
	stopped := make(chan struct{})
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		var request []byte
		if websocket.Message.Receive(ws, &request) != nil {
			return
		}
		if websocket.Message.Send(ws, append([]byte{1}, []byte("{\"type\":\"control_response\",\"response\":{\"subtype\":\"success\",\"request_id\":\"initialize-process\"}}\n")...)) != nil {
			return
		}
		if websocket.Message.Receive(ws, &request) != nil {
			return
		}
		for {
			select {
			case frame := <-frames:
				if websocket.Message.Send(ws, append([]byte{1}, []byte(frame+"\n")...)) != nil {
					return
				}
			case <-stopped:
				return
			case <-ctx.Done():
				return
			}
		}
	}))
	defer func() { close(stopped); server.Close() }()
	records := &nativeRecords{rejectInputCompletion: true}
	replicas := newNativeReplicas(t)
	manager := replicas.replica("replica", records, newTestDelivery(t, records, records))
	buffer := replicas.inputs()
	target := command.ConnectExecution{WorkID: "work-admission-" + uuid.NewString(), Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	input := command.BufferedInput{ID: uuid.NewString(), Text: "start work"}
	// Input buffered before the process is ready waits for readiness.
	_, err := buffer.Append(ctx, target.WorkID, input)
	require.NoError(t, err)
	require.NoError(t, manager.Connect(ctx, target))
	var turn application.CurrentTurn
	require.Eventually(t, func() bool {
		turn, err = manager.GetCurrentTurn(ctx, target.WorkID)
		return err == nil && turn.Active
	}, 5*time.Second, 10*time.Millisecond, "the buffered input is written once the Harness is ready")
	require.Empty(t, turn.ID, "transport admission does not fabricate native turn identity")
	current, err := manager.ObserveProcess(ctx, target.WorkID, target.ProcessID)
	require.NoError(t, err)
	require.Zero(t, current.Turn.Sequence, "admitted stdin is not root activity")
	sendFrame := func(frame string) {
		t.Helper()
		select {
		case frames <- frame:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	sendFrame(`{"type":"user","uuid":"` + input.ID + `","isReplay":true,"message":{"content":"start work"}}`)
	// A following root result proves the receipt was consumed. Neither it nor
	// the receipt may settle the command without its terminal disposition.
	sendFrame(`{"type":"result","uuid":"root-result","subtype":"success","result":"done"}`)
	require.Eventually(t, func() bool {
		current, err = manager.ObserveProcess(ctx, target.WorkID, target.ProcessID)
		return err == nil && current.Turn.Completed
	}, 3*time.Second, 10*time.Millisecond)
	buffered, err := buffer.List(ctx, target.WorkID)
	require.NoError(t, err)
	require.Equal(t, []command.BufferedInput{input}, buffered, "native receipt and a Turn result do not settle the input")
	sendFrame(`{"type":"command_lifecycle","command_uuid":"` + input.ID + `","state":"completed"}`)
	require.Eventually(t, func() bool {
		buffered, err = buffer.List(ctx, target.WorkID)
		return err == nil && len(buffered) == 0
	}, 3*time.Second, 10*time.Millisecond, "settlement leaves the buffer, which Agent reads when its feedback is lost")
	// Agent hears of the settlement asynchronously, after the buffer empties.
	require.Eventually(t, func() bool { records.mu.Lock(); defer records.mu.Unlock(); return records.lostInputCompletion }, 5*time.Second, 10*time.Millisecond)
}

// Original inputs reach Work history, including internal notifications as
// Harness notes. Only marked Skill bodies are hidden; tool outcomes retain
// their own attribution.
func TestInputsRemainVisibleExceptSkillBodies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	transcript := strings.Join([]string{
		`{"type":"control_response","response":{"request_id":"initialize-process"}}`,
		`{"type":"control_response","response":{"subtype":"future","request_id":"initialize-process"}}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"initialize-process"}}`,
		`not json`,
		`{"type":"assistant","uuid":42}`,
		`{"future_field":"unsupported"}`,
		`{"type":"assistant","session_id":"another-session"}`,
		`{"type":"control_request","request":{"subtype":"can_use_tool","tool_name":"Bash"}}`,
		`{"type":"assistant","uuid":"bad-content","message":{"content":42}}`,
		`{"type":"assistant","uuid":"assistant","message":{"content":[{"type":"tool_use","id":"background-tool","name":"Bash","input":{"command":"printf done"}}]}}`,
		`{"type":"user","uuid":"native-notification","isReplay":true,"isSynthetic":true,"message":{"content":"<task-notification>Background task completed</task-notification>"}}`,
		`{"type":"user","uuid":"native-interruption","message":{"content":"[Request interrupted by user for tool use]"}}`,
		`{"type":"user","uuid":"skill-context","isMeta":true,"message":{"content":"Base directory for this skill: /skills/example\nOriginal SKILL body"}}`,
		`{"type":"user","uuid":"skill-stream","isReplay":true,"isSynthetic":true,"message":{"content":[{"type":"text","text":"Base directory for this skill: /skills/example\nRealtime Skill body"}]}}`,
		`{"type":"user","uuid":"native-output","isReplay":true,"isSynthetic":true,"message":{"content":[{"type":"tool_result","tool_use_id":"background-tool","content":"done"}]}}`,
		`{"type":"result","subtype":"success","uuid":"result","result":"Finished"}`,
	}, "\n") + "\n"
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		var request []byte
		if websocket.Message.Receive(ws, &request) != nil {
			return
		}
		if websocket.Message.Send(ws, append([]byte{1}, transcript...)) != nil {
			return
		}
		_ = websocket.Message.Receive(ws, &request)
	}))
	defer server.Close()
	records := &nativeRecords{}
	replicas := newNativeReplicas(t)
	manager := replicas.replica("replica", records, newTestDelivery(t, records, &delayedResultPublisher{nativeRecords: records}))
	target := command.ConnectExecution{WorkID: "work-background", Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, manager.Connect(ctx, target))
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, m := range records.messages {
			if m.GetResult() != nil {
				return true
			}
		}
		return false
	}, 5*time.Second, 10*time.Millisecond, "background notification must not terminate the stream")
	// The handover snapshots the exact position reached.
	require.NoError(t, manager.Shutdown(ctx))
	records.mu.Lock()
	defer records.mu.Unlock()
	var kinds []dispatcherpublicv1.UnifiedMessageKind
	var notes []string
	for _, m := range records.messages {
		kinds = append(kinds, m.Kind)
		if m.Kind == dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_HARNESS_NOTE {
			notes = append(notes, m.GetMessage().Content[0].Text)
		}
	}
	// The Harness wrote these itself; they stay visible, but not as a person's input.
	require.Equal(t, []string{"<task-notification>Background task completed</task-notification>", "[Request interrupted by user for tool use]"}, notes)
	require.Equal(t, []dispatcherpublicv1.UnifiedMessageKind{dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_ACTION, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_HARNESS_NOTE, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_HARNESS_NOTE, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_RESULT}, kinds)
	stored := replicas.stored(target)
	require.Empty(t, stored.Projection.PendingInputs, "internal notifications must not accumulate as pending human inputs")
	require.Equal(t, int64(len(transcript)), stored.Offset, "discarded messages advance the resume position")
	for _, observation := range records.observations {
		require.NotEqual(t, command.ExecutionExited, observation.Kind, "message conversion cannot end the process")
	}
}

// Publishing can finish after the result has already reached message storage.
type delayedResultPublisher struct{ *nativeRecords }

func (p *delayedResultPublisher) PublishLive(ctx context.Context, message *dispatcherpublicv1.UnifiedMessage) error {
	if message.GetResult() != nil {
		timer := time.NewTimer(200 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.nativeRecords.PublishLive(ctx, message)
}

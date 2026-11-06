//go:build integration

package dispatcher_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"golang.org/x/net/websocket"
)

// A controlled native peer separates reply delivery, native acknowledgement
// and tool completion. Only native evidence may end the human wait.
func TestPermissionReplyResolvesBeforeToolExecutionCompletes(t *testing.T) {
	testPermissionReply(t, false)
}
func TestPermissionReplyRetriesUnavailableFeedbackAfterNativeDecision(t *testing.T) {
	testPermissionReply(t, true)
}
func testPermissionReply(t *testing.T, loseFeedback bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	received := make(chan []byte, 2)
	acknowledge := make(chan struct{})
	var initialized atomic.Bool
	stopped := make(chan struct{})
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if websocket.Message.Send(ws, `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`) != nil {
			return
		}
		if !initialized.Swap(true) {
			var initialize []byte
			if websocket.Message.Receive(ws, &initialize) != nil {
				return
			}
			var request struct {
				RequestID string `json:"request_id"`
			}
			if len(initialize) < 2 || json.Unmarshal(initialize[1:], &request) != nil {
				return
			}
			ready, _ := json.Marshal(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": request.RequestID}})
			pending := `{"type":"control_request","request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"tool-one","title":"Run command?","permission_suggestions":[{"type":"addRules","destination":"session","behavior":"allow","rules":[{"toolName":"Bash","ruleContent":"fixture-only"}]}],"input":{"command":"fixture-only"}}}`
			rootCall := `{"type":"assistant","uuid":"root-call","message":{"content":[{"type":"tool_use","id":"tool-one","name":"Bash","input":{"command":"fixture-only"}}]}}`
			// A supplied native member result must not terminate the root turn.
			childResult := `{"type":"result","uuid":"child-result","agent_id":"native-member","subtype":"success","result":"Child done"}`
			if websocket.Message.Send(ws, append([]byte{1}, []byte(string(ready)+"\n"+rootCall+"\n"+childResult+"\n"+pending+"\n")...)) != nil {
				return
			}
		}
		var reply []byte
		if websocket.Message.Receive(ws, &reply) != nil {
			return
		}
		received <- reply
		select {
		case <-acknowledge:
		case <-ctx.Done():
			return
		}
		if websocket.Message.Send(ws, append([]byte{1}, []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"permission","response":{"behavior":"allow"}}}`+"\n")...)) != nil {
			return
		}
		select {
		case <-stopped:
		case <-ctx.Done():
		}
	}))
	defer func() { close(stopped); server.Close() }()
	records := &nativeRecords{}
	feedback := &permissionFeedback{nativeRecords: records}
	feedback.reject.Store(loseFeedback)
	replicas := newNativeReplicas(t)
	manager := replicas.replica("replica", feedback, newTestDelivery(t, records, records))
	buffer := replicas.inputs()
	ref := "permission-work-" + uuid.NewString()
	target := command.ConnectExecution{WorkID: ref, Endpoint: server.URL, AccessToken: "fixture", ProcessID: "process", NativeSessionID: uuid.NewString()}
	require.NoError(t, manager.Connect(ctx, target))
	records.awaitWork(t, ref, command.ExecutionUserInteractionRequired)
	pending, err := manager.ListPendingInteractions(ctx, ref)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, "Run command?", pending[0].Prompt)
	require.True(t, pending[0].CanAlwaysAllow)
	turn, err := manager.GetCurrentTurn(ctx, ref)
	require.NoError(t, err)
	require.NotEmpty(t, turn.ID, "a team member result must not end the root turn")
	require.Equal(t, pending[0].TurnID, turn.ID)
	require.Empty(t, pending[0].QuestionInputJSON, "permission snapshots do not copy tool bodies")

	reply := command.BufferedInput{ID: "permission", Reply: &command.BufferedReply{Allow: true}}
	handOff, err := buffer.Append(ctx, ref, reply)
	require.NoError(t, err)
	require.Equal(t, command.InputBuffered, handOff)
	select {
	case reply := <-received:
		require.Contains(t, string(reply), `"behavior":"allow"`)
	case <-ctx.Done():
		t.Fatal("native peer did not receive permission")
	}
	records.mu.Lock()
	for _, fact := range records.observations {
		require.NotEqual(t, command.ExecutionUserInteractionResolved, fact.Kind, "writing a reply is not native acknowledgement")
	}
	records.mu.Unlock()
	pending, err = manager.ListPendingInteractions(ctx, ref)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.True(t, pending[0].ReplySubmitted)
	close(acknowledge)
	deliveries := int32(1)
	if loseFeedback {
		require.Eventually(t, func() bool {
			pending, err := manager.ListPendingInteractions(ctx, ref)
			return err == nil && len(pending) == 0 && feedback.resolved.Load() == 2
		}, 3*time.Second, 10*time.Millisecond, "native acknowledgement advances and its unavailable feedback is retried")
		deliveries = 2
	} else {
		require.Eventually(t, func() bool {
			records.mu.Lock()
			defer records.mu.Unlock()
			for _, fact := range records.observations {
				if fact.Kind == command.ExecutionUserInteractionResolved && fact.InteractionID == "permission" {
					return true
				}
			}
			return false
		}, 3*time.Second, 10*time.Millisecond, "permission must resolve while tool execution is still blocked")
	}
	require.Eventually(t, func() bool {
		records.mu.Lock()
		defer records.mu.Unlock()
		for _, fact := range records.observations {
			if fact.Kind == command.ExecutionInputSettled && fact.InputID == "permission" && fact.InputOutcome == command.InputCompleted {
				return true
			}
		}
		return false
	}, 3*time.Second, 10*time.Millisecond, "the resolved request settles its buffered reply")
	handOff, err = buffer.Append(ctx, ref, reply)
	require.NoError(t, err)
	require.Equal(t, command.InputSettled, handOff, "a settled reply is remembered, not buffered again")
	require.Empty(t, received, "a resolved reply must not be retransmitted")
	pending, err = manager.ListPendingInteractions(ctx, ref)
	require.NoError(t, err)
	require.Empty(t, pending)
	require.Equal(t, deliveries, feedback.resolved.Load(), "repeated reply must not replay acknowledged feedback")
	require.Eventually(t, func() bool {
		current, err := manager.ObserveProcess(ctx, ref, target.ProcessID)
		return err == nil && current.Ready && current.Turn.Active && current.Turn.Sequence > 0 &&
			len(current.RequiredInteractionIDs) == 0
	}, 3*time.Second, 10*time.Millisecond, "current process observation must recover lost reply and root-turn feedback")
}

type permissionFeedback struct {
	*nativeRecords
	reject   atomic.Bool
	resolved atomic.Int32
}

func (f *permissionFeedback) ReportExecution(ctx context.Context, fact command.ExecutionObservation) error {
	if fact.Kind == command.ExecutionUserInteractionResolved {
		f.resolved.Add(1)
		if f.reject.Swap(false) {
			return command.ErrExecutionFeedbackUnavailable
		}
	}
	return f.nativeRecords.ReportExecution(ctx, fact)
}

func (*permissionFeedback) ReportConnectionEnded(context.Context, string, string) error { return nil }

package nativeprocess

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/pkg/execd"
	"golang.org/x/net/websocket"
)

func replay(offset int64, data string) []byte {
	frame := make([]byte, 9+len(data))
	frame[0] = 3
	binary.BigEndian.PutUint64(frame[1:9], uint64(offset))
	copy(frame[9:], data)
	return frame
}

func protocolConnection(t *testing.T, offset int64, frames ...any) (*Connection, context.Context) {
	t.Helper()
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		require.Equal(t, fmt.Sprint(offset), ws.Request().URL.Query().Get("since"))
		for _, frame := range frames {
			if err := websocket.Message.Send(ws, frame); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	client, err := execd.NewClient(server.Client(), server.URL, "test-token")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	pipe, err := client.Attach(ctx, "process", offset)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pipe.Close() })
	return NewConnection(pipe, "native", offset), ctx
}

func TestNativeReplayContinuesPartialJSONWithoutAdvancingCheckpointEarly(t *testing.T) {
	line := `{"type":"result","session_id":"native"}` + "\n"
	next := `{"type":"control_cancel_request","request_id":"permission","session_id":"native"}` + "\n"
	split := 13
	connection, ctx := protocolConnection(t, 431, replay(431, line[:split]), `{"type":"connected","session_id":"process","mode":"pipe","role":"holder"}`, append([]byte{1}, []byte(line[split:]+next)...))
	connected, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "connected", connected.Kind)
	require.Equal(t, int64(431), connected.Offset, "partial JSON is not an applied protocol message")
	result, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "result", result.Message.Type)
	require.Equal(t, int64(431+len(line)), result.Offset)
	cancel, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "permission", cancel.Message.RequestID)
	require.Equal(t, int64(431+len(line)+len(next)), cancel.Offset)
}

func TestNativeReplayRejectsReplacedBytes(t *testing.T) {
	connection, ctx := protocolConnection(t, 431, replay(430, `{"type":"result"}`+"\n"))
	_, err := connection.Receive(ctx)
	require.ErrorIs(t, err, ErrReplay)
}

func TestNativeReplayDoesNotGuessStreamIdentityForUnexpectedStderr(t *testing.T) {
	connection, ctx := protocolConnection(t, 0, []byte{2, 'e', 'r', 'r'})
	_, err := connection.Receive(ctx)
	require.ErrorIs(t, err, ErrProtocol)
}

func TestNativeMessagesDiscardInvalidAndOversizedLinesAndContinue(t *testing.T) {
	for _, invalid := range []string{"not json", `{"type":"assistant","uuid":42}`, strings.Repeat("x", maxMessageBytes+1)} {
		line := invalid + "\n"
		result := `{"type":"result","session_id":"native"}` + "\n"
		// Split before the newline to exercise bounded oversized-line draining.
		connection, ctx := protocolConnection(t, 0, append([]byte{1}, invalid...), append([]byte{1}, []byte("\n"+result)...))
		discarded, err := connection.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, "discarded", discarded.Kind)
		require.Equal(t, int64(len(line)), discarded.Offset)
		next, err := connection.Receive(ctx)
		require.NoError(t, err)
		require.Equal(t, "result", next.Message.Type)
		require.Equal(t, int64(len(line)+len(result)), next.Offset)
	}
}

func TestNativeTruncatedMessageDoesNotHideProcessExit(t *testing.T) {
	partial := `{"type":`
	connection, ctx := protocolConnection(t, 0, append([]byte{1}, partial...), `{"type":"exit","exit_code":1}`)
	event, err := connection.Receive(ctx)
	require.NoError(t, err)
	require.Equal(t, "exit", event.Kind)
	require.Equal(t, 1, event.ExitCode)
	require.True(t, event.Discarded)
	require.Equal(t, int64(len(partial)), event.Offset)
}

func TestAssistantLookaheadUsesTheImmediatelyFollowingRawLog(t *testing.T) {
	assistant := `{"type":"assistant","uuid":"answer","message":{"content":[{"type":"text","text":"Original answer"}]}}` + "\n"
	for _, tc := range []struct {
		name, next string
		repeated   bool
	}{
		{"same result", `{"type":"result","subtype":"success","result":"Original answer"}`, true},
		{"different result", `{"type":"result","subtype":"success","result":"Different answer"}`, false},
		{"unshown system log", `{"type":"system","subtype":"hook_started"}`, false},
		{"permission", `{"type":"control_request","request_id":"permission"}`, false},
		{"another agent", `{"type":"result","agent_id":"child","result":"Original answer"}`, false},
		{"empty error result", `{"type":"result","is_error":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connection, ctx := protocolConnection(t, 0, append([]byte{1}, []byte(assistant+tc.next+"\n")...))
			first, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, "assistant", first.Message.Type)
			require.Equal(t, tc.repeated, first.RepeatedAssistant)
			require.Equal(t, int64(len(assistant)), first.Offset, "lookahead must not commit the following log")
			next, err := connection.Receive(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.next, string(next.Raw), "never consume or rewrite the following log")
		})
	}
}

func TestAssistantLookaheadTimeoutFlushesWithoutClosingThePipe(t *testing.T) {
	release := make(chan struct{})
	assistant := `{"type":"assistant","uuid":"answer","message":{"content":[{"type":"text","text":"Original answer"}]}}` + "\n"
	result := `{"type":"result","subtype":"success","result":"Original answer"}` + "\n"
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		if websocket.Message.Send(ws, append([]byte{1}, []byte(assistant)...)) != nil {
			return
		}
		<-release
		_ = websocket.Message.Send(ws, append([]byte{1}, []byte(result)...))
	}))
	defer server.Close()
	client, err := execd.NewClient(server.Client(), server.URL, "fixture")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pipe, err := client.Attach(ctx, "process", 0)
	require.NoError(t, err)
	defer pipe.Close()
	connection := NewConnection(pipe, "native", 0)
	first, err := connection.Receive(ctx)
	close(release)
	require.NoError(t, err, "assistant must flush before another log exists")
	require.False(t, first.RepeatedAssistant)
	require.Equal(t, "assistant", first.Message.Type)
	next, err := connection.Receive(ctx)
	require.NoError(t, err, "timeout must leave the pipe readable")
	require.Equal(t, "result", next.Message.Type)
}

// writtenInput captures the stdin line the connection writes for one input.
func writtenInput(t *testing.T, write func(*Connection, context.Context) error) map[string]any {
	t.Helper()
	received := make(chan []byte, 1)
	server := httptest.NewServer(websocket.Handler(func(ws *websocket.Conn) {
		defer ws.Close()
		var frame []byte
		if err := websocket.Message.Receive(ws, &frame); err == nil {
			received <- frame
		}
	}))
	t.Cleanup(server.Close)
	client, err := execd.NewClient(server.Client(), server.URL, "test-token")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	pipe, err := client.Attach(ctx, "process", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pipe.Close() })
	require.NoError(t, write(NewConnection(pipe, "native", 0), ctx))
	select {
	case frame := <-received:
		var line map[string]any
		require.NoError(t, json.Unmarshal(frame[1:], &line))
		return line
	case <-ctx.Done():
		t.Fatal("no input written")
		return nil
	}
}

// Input reaches the Harness exactly as submitted, a command included.
func TestUserInputWritesTheTextUnchanged(t *testing.T) {
	for _, text := range []string{"Ship it on Friday.", "/compact"} {
		line := writtenInput(t, func(c *Connection, ctx context.Context) error {
			return c.UserInput(ctx, "0199a1b2-0000-7000-8000-000000000001", text)
		})
		require.Equal(t, text, line["message"].(map[string]any)["content"])
	}
}

// A denial's reason and a human's answers reach the Agent as given.
func TestToolResponseCarriesTheHumanAnswerUnchanged(t *testing.T) {
	question := &Message{Type: "control_request", RequestID: "question-1"}
	question.Request.Subtype = "can_use_tool"
	question.Request.ToolName = "AskUserQuestion"
	question.Request.Input = json.RawMessage(`{"questions":[{"question":"Proceed?"}]}`)
	answered, err := NewToolResponse(question, true, false, "", json.RawMessage(`{"questions":[{"question":"Proceed?"}],"answers":{"Proceed?":"Yes"}}`))
	require.NoError(t, err)
	var written struct {
		Response struct {
			Response struct {
				Message      string `json:"message"`
				UpdatedInput struct {
					Answers map[string]string `json:"answers"`
				} `json:"updatedInput"`
			} `json:"response"`
		} `json:"response"`
	}
	require.NoError(t, json.Unmarshal(answered.data, &written))
	require.Equal(t, map[string]string{"Proceed?": "Yes"}, written.Response.Response.UpdatedInput.Answers)

	denied, err := NewToolResponse(question, false, false, "Not this time", nil)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(denied.data, &written))
	require.Equal(t, "Not this time", written.Response.Response.Message)
}

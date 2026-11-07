package nativeprocess_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/application/command"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCommandInputFallbackPreservesNativeEcho(t *testing.T) {
	buffered := []command.BufferedInput{{ID: "input", Text: "/plugin:check src --strict"}}
	state := *nativeprocess.NewProtocolState()
	state.MirrorBuffer(buffered)
	project := func(raw string, at int64) []*dispatcherpublicv1.UnifiedMessage {
		t.Helper()
		var message nativeprocess.Message
		require.NoError(t, json.Unmarshal([]byte(raw), &message))
		messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
		require.NoError(t, err)
		messages = state.AppendCommandInput("work", &message, messages)
		for _, item := range messages {
			item.Timestamp = timestamppb.New(time.Unix(at, 0))
		}
		return state.Projection.Attribute(&message, messages, "session", "process", at)
	}
	require.Empty(t, project(`{"type":"command_lifecycle","command_uuid":"input","state":"queued"}`, 1))
	started := project(`{"type":"command_lifecycle","command_uuid":"input","state":"started"}`, 2)
	require.Len(t, started, 1)
	require.Equal(t, "/plugin:check src --strict", started[0].GetMessage().Content[0].Text)
	require.NotEmpty(t, started[0].TurnId)
	retained, err := json.Marshal(state)
	require.NoError(t, err)
	state = nativeprocess.ProtocolState{}
	require.NoError(t, json.Unmarshal(retained, &state))
	// A reader resuming the stream reloads the buffer; the input is buffered
	// until it settles.
	state.MirrorBuffer(buffered)
	// A delayed native echo keeps its own content, with the same input identity,
	// turn and first timestamp even after reconstitution.
	echo := project(`{"type":"user","uuid":"input","isReplay":true,"message":{"content":"Native command echo"}}`, 3)
	require.Len(t, echo, 1)
	require.Equal(t, "Native command echo", echo[0].GetMessage().Content[0].Text)
	require.Equal(t, started[0].Id, echo[0].Id)
	require.Equal(t, started[0].TurnId, echo[0].TurnId)
	require.Equal(t, started[0].Timestamp, echo[0].Timestamp)
	result := project(`{"type":"result","uuid":"result","subtype":"success","result":"Unknown command: /plugin:check"}`, 4)
	require.Len(t, result, 1)
	require.Equal(t, started[0].TurnId, result[0].TurnId)
	require.Empty(t, project(`{"type":"command_lifecycle","command_uuid":"input","state":"completed"}`, 5))
	require.Empty(t, project(`{"type":"user","uuid":"generated","isSynthetic":true,"message":{"content":"Base directory for this skill: /skills/example\nSkill instructions"}}`, 6))
	unbuffered := project(`{"type":"user","uuid":"unbuffered","message":{"content":"Original native text"}}`, 6)
	require.Len(t, unbuffered, 1)
	require.Equal(t, "Original native text", unbuffered[0].GetMessage().Content[0].Text)
	tool := project(`{"type":"user","uuid":"tool-result","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":"tool output"}]}}`, 7)
	require.Len(t, tool, 1)
	require.NotEqual(t, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_USER, tool[0].Kind)
}

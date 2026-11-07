package nativeprocess_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/nativeprocess"
	"google.golang.org/protobuf/proto"
)

func projectFrame(t *testing.T, state *nativeprocess.MessageProjection, raw string) []*dispatcherpublicv1.UnifiedMessage {
	t.Helper()
	var event nativeprocess.Message
	require.NoError(t, json.Unmarshal([]byte(raw), &event))
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
	require.NoError(t, err)
	return state.Attribute(&event, messages, "harness", "process", 100)
}

func TestProjectionGroupsMergedInputsAcrossCheckpointRecovery(t *testing.T) {
	state := nativeprocess.MessageProjection{}
	require.Empty(t, projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"first","state":"started"}`))
	first := projectFrame(t, &state, `{"type":"user","uuid":"first","isReplay":true,"message":{"content":"First input"}}`)
	require.Len(t, first, 1)
	require.NotEmpty(t, first[0].TurnId)
	require.NotNil(t, first[0].Agent)
	require.Empty(t, *first[0].Agent)
	result := projectFrame(t, &state, `{"type":"result","uuid":"result-first","subtype":"success","result":"First result"}`)
	require.Equal(t, first[0].TurnId, result[0].TurnId)
	// The queued input echo can precede its started fact. It must not inherit the finished turn.
	require.Empty(t, projectFrame(t, &state, `{"type":"user","uuid":"second","isReplay":true,"message":{"content":"Second input"}}`))
	saved, err := json.Marshal(state)
	require.NoError(t, err)
	state = nativeprocess.MessageProjection{}
	require.NoError(t, json.Unmarshal(saved, &state))
	second := projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"second","state":"started"}`)
	require.Len(t, second, 1)
	require.Equal(t, "Second input", second[0].GetMessage().Content[0].Text)
	require.NotEqual(t, first[0].TurnId, second[0].TurnId)
	require.Empty(t, projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"third","state":"started"}`))
	third := projectFrame(t, &state, `{"type":"user","uuid":"third","isReplay":true,"message":{"content":"Third input"}}`)
	require.Equal(t, second[0].TurnId, third[0].TurnId)
	last := projectFrame(t, &state, `{"type":"result","uuid":"result-last","subtype":"success","result":"Merged result"}`)
	require.Equal(t, second[0].TurnId, last[0].TurnId)
	repeated := projectFrame(t, &state, `{"type":"user","uuid":"second","isReplay":true,"message":{"content":"Second input"}}`)
	require.Equal(t, second[0].Id, repeated[0].Id)
	require.Equal(t, second[0].TurnId, repeated[0].TurnId)
	require.True(t, proto.Equal(second[0], repeated[0]), "input replay preserves its first arrival time and content")
}

func TestProjectionRetainsDelegationOwnershipForLateChildAndToolOutput(t *testing.T) {
	state := nativeprocess.MessageProjection{}
	projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"first","state":"started"}`)
	call := projectFrame(t, &state, `{"type":"assistant","uuid":"call","message":{"content":[{"type":"tool_use","id":"delegate","name":"Agent","input":{"prompt":"Find facts"}}]}}`)
	projectFrame(t, &state, `{"type":"system","subtype":"task_started","task_type":"local_agent","task_id":"native-child-1","tool_use_id":"delegate"}`)
	child := projectFrame(t, &state, `{"type":"assistant","uuid":"child-text","parent_tool_use_id":"delegate","message":{"content":[{"type":"text","text":"Child facts"}]}}`)
	require.NotNil(t, child[0].Agent)
	require.Equal(t, "native-child-1", *child[0].Agent)
	require.NotEqual(t, call[0].TurnId, child[0].TurnId)
	projectFrame(t, &state, `{"type":"result","uuid":"root-result","subtype":"success","result":"Root done"}`)
	projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"next","state":"started"}`)
	late := projectFrame(t, &state, `{"type":"assistant","uuid":"late","parent_tool_use_id":"delegate","message":{"content":[{"type":"text","text":"Late child facts"}]}}`)
	require.Equal(t, child[0].TurnId, late[0].TurnId)
	output := projectFrame(t, &state, `{"type":"user","uuid":"tool-result","message":{"content":[{"type":"tool_result","tool_use_id":"delegate","content":"Collected facts"}]}}`)
	require.Equal(t, call[0].TurnId, output[0].TurnId)
	require.NotNil(t, output[0].Agent)
	require.Empty(t, *output[0].Agent)
	unknown := projectFrame(t, &state, `{"type":"assistant","uuid":"unknown","parent_tool_use_id":"unseen-delegation","message":{"content":[{"type":"text","text":"Unknown owner"}]}}`)
	require.Nil(t, unknown[0].Agent)
	require.Empty(t, unknown[0].TurnId)
}

func TestUnstartedInputAndUnknownAgentNeverInheritCurrentTurn(t *testing.T) {
	state := nativeprocess.MessageProjection{}
	projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"active","state":"started"}`)
	require.Empty(t, projectFrame(t, &state, `{"type":"user","uuid":"queued","isReplay":true,"message":{"content":"Canceled queued input"}}`))
	canceled := projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"queued","state":"cancelled"}`)
	require.Len(t, canceled, 1)
	require.Empty(t, canceled[0].TurnId)
	require.Equal(t, "Canceled queued input", canceled[0].GetMessage().Content[0].Text)
	repeated := projectFrame(t, &state, `{"type":"user","uuid":"queued","isReplay":true,"message":{"content":"Canceled queued input"}}`)
	require.Len(t, repeated, 1, "a terminal input does not wait for a started event that cannot arrive")
	require.True(t, proto.Equal(canceled[0], repeated[0]))
	other := projectFrame(t, &state, `{"type":"assistant","uuid":"team-text","agent_id":"native-team-member","message":{"content":[{"type":"text","text":"No turn evidence"}]}}`)
	require.Equal(t, "native-team-member", other[0].GetAgent())
	require.Empty(t, other[0].TurnId)
}

// Claude 2.1.266 reuses the original parent_tool_use_id after SendMessage
// resumes a member. task_started identifies the same native task with a new call.
func TestResumedMemberUsesNewTurnWithOriginalParentReference(t *testing.T) {
	state := nativeprocess.MessageProjection{}
	projectFrame(t, &state, `{"type":"command_lifecycle","command_uuid":"root-input","state":"started"}`)
	projectFrame(t, &state, `{"type":"system","subtype":"task_started","task_type":"local_agent","task_id":"member","tool_use_id":"delegate"}`)
	first := projectFrame(t, &state, `{"type":"assistant","uuid":"member-first","parent_tool_use_id":"delegate","message":{"content":[{"type":"text","text":"First answer"},{"type":"tool_use","id":"old-read","name":"Read","input":{"file_path":"notes.md"}}]}}`)
	projectFrame(t, &state, `{"type":"system","subtype":"task_started","task_type":"local_agent","task_id":"member","tool_use_id":"follow-up"}`)
	second := projectFrame(t, &state, `{"type":"assistant","uuid":"member-second","parent_tool_use_id":"delegate","message":{"content":[{"type":"text","text":"Follow-up answer"}]}}`)
	require.Equal(t, "member", second[0].GetAgent())
	require.NotEmpty(t, second[0].TurnId)
	require.NotEqual(t, first[0].TurnId, second[0].TurnId, "the resumed member starts another native turn")
	lateTool := projectFrame(t, &state, `{"type":"user","uuid":"late-output","parent_tool_use_id":"delegate","message":{"content":[{"type":"tool_result","tool_use_id":"old-read","content":"Original file content"}]}}`)
	require.Equal(t, first[0].TurnId, lateTool[0].TurnId, "late tool output stays with its original call")
}

// Text and its tool call often share a millisecond; their IDs keep output
// order, and a replay of the same lines yields the same IDs.
func TestProjectedMessageIDsFollowOutputOrderAndSurviveReplay(t *testing.T) {
	lines := []string{
		`{"type":"assistant","uuid":"zz-text","message":{"content":[{"type":"text","text":"Reading the notes"}]}}`,
		`{"type":"assistant","uuid":"aa-call","message":{"content":[{"type":"tool_use","id":"read","name":"Read","input":{"file_path":"notes.md"}}]}}`,
		`{"type":"user","uuid":"mm-output","message":{"content":[{"type":"tool_result","tool_use_id":"read","content":"Notes"}]}}`,
	}
	project := func(process string) []string {
		state := nativeprocess.MessageProjection{}
		var ids []string
		for index, raw := range lines {
			var event nativeprocess.Message
			require.NoError(t, json.Unmarshal([]byte(raw), &event))
			messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
			require.NoError(t, err)
			for _, message := range state.Attribute(&event, messages, "harness", process, int64(900+index*100)) {
				ids = append(ids, message.Id)
			}
		}
		return ids
	}
	ids := project("process")
	require.Len(t, ids, 3)
	require.True(t, slices.IsSorted(ids), "IDs follow output order: %v", ids)
	require.Equal(t, ids, project("process"), "a replay of the same output keeps its IDs")
	require.NotContains(t, project("next-process"), ids[0], "another process never reuses an ID")
}

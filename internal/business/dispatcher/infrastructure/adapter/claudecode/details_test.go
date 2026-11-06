package claudecode_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestLargeOriginalInputIsTheDetailNotTheReadableMessage(t *testing.T) {
	input := `{"file_path":"/work/a.md", "exact_number":9007199254740993,"content":"` + strings.Repeat("原文", 100000) + `"}`
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call","name":"Write","input":`+input+`}]}}`))
	require.NoError(t, err)
	require.Len(t, messages, 1)
	projected := messages[0]
	require.Equal(t, input, projected.Detail)
	require.Equal(t, "/work/a.md", projected.GetAction().Summary)
	require.Equal(t, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_JSON, projected.DetailFormat)
	require.Less(t, len(readable(t, projected)), 1024)
	messages, _, err = claudecode.UpstreamToMessages(context.Background(), "work", []byte(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call","content":""}]}}`))
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, messages[0].DetailFormat)
	require.Empty(t, messages[0].Detail)
	require.Zero(t, messages[0].DetailLength)
}

func TestSkillNameIsAvailableWithoutLoadingToolInput(t *testing.T) {
	for _, name := range []string{"event-storming", "ddd-expert:event-storming"} {
		input := `{"skill":"` + name + `","args":"Original arguments"}`
		messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"skill-call","name":"Skill","input":`+input+`}]}}`))
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Equal(t, "Skill", messages[0].GetAction().ToolName)
		require.Equal(t, name, messages[0].GetAction().Summary)
		require.Equal(t, input, messages[0].Detail)
	}
}

func TestQuestionRequestReferencesOriginalToolInput(t *testing.T) {
	raw := `{"type":"control_request","request_id":"question-request","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion","tool_use_id":"question-tool","input":{"questions":[{"question":"Proceed?","header":"Choice","options":[{"label":"Yes","description":"Continue"}],"multiSelect":false}]}}}`
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
	require.NoError(t, err)
	require.Len(t, messages, 1)
	question := messages[0].GetInteraction()
	require.NotNil(t, question)
	require.Equal(t, "question-request", question.InteractionId)
	require.Equal(t, "question-tool", question.ToolCallId)
	require.Equal(t, dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_REQUESTED, question.Phase)
	require.Empty(t, messages[0].Detail, "the original questions are stored once in the linked tool input")
	require.EqualValues(t, 1, question.QuestionCount)
	outcome, err := claudecode.ControlOutcomeToMessage("work", []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"question-request","response":{"behavior":"allow","updatedInput":{"answers":{}}}}}`), "AskUserQuestion", "question-tool", json.RawMessage(`{"questions":[{"question":"Proceed?"}]}`))
	require.NoError(t, err)
	require.EqualValues(t, 1, outcome.GetInteraction().QuestionCount)
	require.JSONEq(t, `{}`, outcome.GetInteraction().ResponseJson, "an omitted answer is skipped, not fabricated")
	answered, err := claudecode.ControlOutcomeToMessage("work", []byte(`{"type":"control_response","response":{"subtype":"success","request_id":"question-request","response":{"behavior":"allow","updatedInput":{"answers":{"Proceed?":"Yes"}}}}}`), "AskUserQuestion", "question-tool", json.RawMessage(`{"questions":[{"question":"Proceed?"}]}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"Proceed?":"Yes"}`, answered.GetInteraction().ResponseJson)
}

func TestControlFailureStaysWithItsInteraction(t *testing.T) {
	raw := []byte(`{"type":"control_response","response":{"subtype":"error","request_id":"request","error":"Original failure text"}}`)
	for _, tool := range []string{"Bash", "AskUserQuestion"} {
		t.Run(tool, func(t *testing.T) {
			message, err := claudecode.ControlOutcomeToMessage("work", raw, tool, "tool", nil)
			require.NoError(t, err)
			require.Nil(t, message.GetError(), "a reply failure is not a model-service failure")
			if tool == "Bash" {
				require.Equal(t, dispatcherpublicv1.PermissionPhase_PERMISSION_PHASE_FAILED, message.GetPermission().Phase)
				require.Equal(t, "Original failure text", message.GetPermission().Reason)
			} else {
				require.Equal(t, dispatcherpublicv1.InteractionPhase_INTERACTION_PHASE_FAILED, message.GetInteraction().Phase)
				require.Equal(t, "Original failure text", message.GetInteraction().Error)
			}
		})
	}
	for _, raw := range []string{
		`{"type":"control_response","response":{"subtype":"success","request_id":"unknown","response":{"behavior":"allow"}}}`,
		string(raw),
	} {
		messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(raw))
		require.NoError(t, err)
		require.Empty(t, messages, "uncorrelated control acknowledgements do not manufacture interactions")
	}
}

func TestLongHarnessErrorHasBoundedPreviewAndOriginalDetail(t *testing.T) {
	original := strings.Repeat("服务不可用\n", 10000)
	raw, err := json.Marshal(map[string]any{"type": "auth_status", "error": original})
	require.NoError(t, err)
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", raw)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	message := messages[0]
	require.Equal(t, original, message.Detail)
	require.Equal(t, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, message.DetailFormat)
	require.EqualValues(t, utf8.RuneCountInString(original), message.DetailLength)
	require.True(t, strings.HasPrefix(original, message.GetError().Message))
	require.Less(t, len(readable(t, message)), 8192, "list/live projection must not carry the full diagnostic")
}

func BenchmarkProductProjection(b *testing.B) {
	for name, raw := range map[string]string{
		"thinking_tokens": `{"type":"system","subtype":"thinking_tokens","thinking_tokens":1234,"uuid":"native-event"}`,
		"stream_delta":    `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"` + strings.Repeat("x", 16384) + `"}}}`,
		"write_input":     `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"write","name":"Write","input":{"file_path":"/work/a","content":"` + strings.Repeat("x", 300000) + `"}}]}}`,
	} {
		b.Run(name, func(b *testing.B) {
			input := []byte(raw)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := claudecode.UpstreamToMessages(context.Background(), "work", input); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// readable encodes what history lists and live readers receive: the message
// without its detail, which is fetched on demand.
func readable(t *testing.T, message *dispatcherpublicv1.UnifiedMessage) []byte {
	t.Helper()
	listed := proto.CloneOf(message)
	listed.Detail = ""
	encoded, err := protojson.Marshal(listed)
	require.NoError(t, err)
	return encoded
}

// A tool result's observation names the Harness position of the message that
// holds it, from where another Work may start its context.
func TestToolResultNamesItsContextPosition(t *testing.T) {
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(`{"type":"user","uuid":"5f1c2b0e-0000-4000-8000-000000000001","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ok"},{"type":"tool_result","tool_use_id":"toolu_2","content":"ok"}]}}`))
	require.NoError(t, err)
	require.Len(t, messages, 2)
	for _, message := range messages {
		require.Equal(t, "5f1c2b0e-0000-4000-8000-000000000001", message.GetObservation().GetContextPosition())
	}
	require.Equal(t, "toolu_2", messages[1].GetObservation().GetToolCallId())
}

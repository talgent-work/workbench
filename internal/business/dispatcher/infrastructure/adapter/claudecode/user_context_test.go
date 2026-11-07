package claudecode

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
)

// Only marked Skill bodies are hidden. Other inputs and tool outcomes retain
// their original content, including mixed frames and literal human Skill text.
func TestUpstreamUserContextMarkers(t *testing.T) {
	for _, marker := range []string{`"isSynthetic":true`, `"isMeta":true`, `"isSynthetic":false,"isMeta":true`} {
		t.Run(marker, func(t *testing.T) {
			for _, content := range []string{`"Base directory for this skill: /skills/example\nPrivate Skill instructions"`, `[{"type":"text","text":"Base directory for this skill: /skills/example\nPrivate Skill instructions"}]`} {
				messages, notice, err := UpstreamToMessages(context.Background(), "work", []byte(`{"type":"user",`+marker+`,"message":{"content":`+content+`}}`))
				require.NoError(t, err)
				require.Nil(t, notice)
				require.Empty(t, messages)
			}
			messages := mustTranslateMessages(t, `{"type":"user","uuid":"native-result",`+marker+`,"parent_tool_use_id":"parent","message":{"content":[{"type":"text","text":"Private context"},{"type":"tool_result","tool_use_id":"call","is_error":true,"content":"Permission denied"},{"type":"text","text":"More private context"}]}}`)
			require.Len(t, messages, 3)
			require.Equal(t, "Private context", messages[0].GetMessage().Content[0].Text)
			require.Equal(t, dispatcherpublicv1.UnifiedMessageKind_UNIFIED_MESSAGE_KIND_OBSERVATION, messages[1].Kind)
			require.Equal(t, "call", messages[1].GetObservation().ToolCallId)
			require.Equal(t, "native-result", messages[1].GetObservation().ContextPosition)
			require.True(t, messages[1].GetObservation().IsError)
			require.Equal(t, "Permission denied", messages[1].Detail)
			require.Equal(t, "More private context", messages[2].GetMessage().Content[0].Text)
			mixed := mustTranslateMessages(t, `{"type":"user",`+marker+`,"message":{"content":[{"type":"text","text":"Base directory for this skill: /skills/example\nSkill instructions"},{"type":"text","text":"Original notification"},{"type":"image","source":{"media_type":"image/png","data":"aW1hZ2U="}}]}}`)
			require.Len(t, mixed, 1)
			require.Len(t, mixed[0].GetMessage().Content, 2)
			require.Equal(t, "Original notification", mixed[0].GetMessage().Content[0].Text)
			require.Equal(t, "aW1hZ2U=", mixed[0].GetMessage().Content[1].Text)
		})
	}
	for _, marker := range []string{"", `"isSynthetic":false,`, `"isMeta":false,`} {
		messages := mustTranslateMessages(t, `{"type":"user",`+marker+`"message":{"content":[{"type":"text","text":"Base directory for this skill: keep my literal input"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aW1hZ2U="}}]}}`)
		require.Len(t, messages, 1)
		blocks := messages[0].GetMessage().Content
		require.Len(t, blocks, 2)
		require.Equal(t, "Base directory for this skill: keep my literal input", blocks[0].Text)
		require.Equal(t, dispatcherpublicv1.ContentBlockType_CONTENT_BLOCK_TYPE_IMAGE, blocks[1].Type)
		require.Equal(t, "aW1hZ2U=", blocks[1].Text)
	}
}

// Claude records a slash command it runs as tags; the Work shows and keeps it
// as the person typed it. Text with anything besides those tags stays as is.
func TestUpstreamSlashCommandEchoShowsTheTypedCommand(t *testing.T) {
	for raw, want := range map[string]string{
		`"<command-message>mattpocock-skills:codebase-design</command-message>\n<command-name>/mattpocock-skills:codebase-design</command-name>"`:              "/mattpocock-skills:codebase-design",
		`[{"type":"text","text":"<command-name>/review</command-name>\n<command-message>review</command-message>\n<command-args>the open PR</command-args>"}]`: "/review the open PR",
		`"<command-message>review</command-message>\n<command-name>/review</command-name>\n<command-args></command-args>"`:                                     "/review",
		`"Run <command-name>/review</command-name> later"`:          "Run <command-name>/review</command-name> later",
		`"<command-message>review</command-message>\nthen explain"`: "<command-message>review</command-message>\nthen explain",
	} {
		messages := mustTranslateMessages(t, `{"type":"user","message":{"content":`+raw+`}}`)
		require.Len(t, messages, 1, raw)
		require.Equal(t, want, messages[0].GetMessage().Content[0].Text, raw)
	}
}

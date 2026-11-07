package claudecode_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
)

// The fixture's frames follow Claude Code 2.1.266 (pinned in
// deploy/sandbox/Dockerfile.base): the stream-json user frame and the
// FileReadOutput, FileWriteOutput and FileEditOutput types of that release's
// sdk-tools.d.ts.
func fileToolFrames(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	content, err := os.ReadFile("testdata/file_tools_claude_2_1_266.json")
	require.NoError(t, err)
	frames := map[string]json.RawMessage{}
	require.NoError(t, json.Unmarshal(content, &frames))
	return frames
}

func observation(t *testing.T, frame []byte) *dispatcherpublicv1.UnifiedMessage {
	t.Helper()
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", frame)
	require.NoError(t, err)
	for _, message := range messages {
		if message.GetObservation() != nil {
			return message
		}
	}
	t.Fatalf("no observation in %s", frame)
	return nil
}

func TestToolPreviewTakesTheCallsOwnResult(t *testing.T) {
	frames := fileToolFrames(t)

	read := observation(t, frames["read_whole"])
	require.Equal(t, "     1\t# Notes\n     2\t\n     3\tBody\n", read.Detail, "the original output stays the detail")
	require.Equal(t, &dispatcherpublicv1.ToolOutputPreview{ToolName: "Read", FilePath: "/workspace/notes.md", Content: "# Notes\n\nBody\n", StartLine: 1, NumLines: 3, TotalLines: 3, TotalLength: 14}, stripped(read.ToolPreview))

	ranged := observation(t, frames["read_range"]).ToolPreview
	require.Equal(t, uint32(41), ranged.StartLine)
	require.Equal(t, uint32(120), ranged.TotalLines)
	require.True(t, observation(t, frames["read_capped"]).ToolPreview.TruncatedByTokenCap)
	require.Nil(t, observation(t, frames["read_image"]).ToolPreview, "a non-text Read has no body to preview")

	write := observation(t, frames["write"])
	require.Equal(t, "File created successfully at: /workspace/report.md", write.Detail, "the model sees only a status")
	require.Equal(t, "Write", write.ToolPreview.ToolName)
	require.Equal(t, "# Report\n\nWritten body\n", write.ToolPreview.Content, "the preview is what was written")

	edit := observation(t, frames["edit"])
	require.Equal(t, "Edit", edit.ToolPreview.ToolName)
	require.Empty(t, edit.ToolPreview.Content)
	require.Len(t, edit.ToolPreview.Hunks, 1)
	require.Equal(t, []string{"-const a = 1;", "+const a = 2;"}, edit.ToolPreview.Hunks[0].Lines)
	stored, err := protojson.Marshal(edit.ToolPreview)
	require.NoError(t, err)
	require.NotContains(t, string(stored), "SECRET", "neither the original file nor the git diff is kept")
}

// A Write's input may differ from what was written; only the output counts.
func TestToolPreviewIgnoresTheInput(t *testing.T) {
	call := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"write-1","name":"Write","input":{"file_path":"/workspace/report.md","content":"INPUT BODY"}}]}}`
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", []byte(call))
	require.NoError(t, err)
	require.Nil(t, messages[0].ToolPreview)
	require.Equal(t, "# Report\n\nWritten body\n", observation(t, fileToolFrames(t)["write"]).ToolPreview.Content)
}

func TestToolPreviewKeepsOnlyTheOriginalOutputWhenUnattributable(t *testing.T) {
	frames := fileToolFrames(t)
	for _, name := range []string{"error", "string_result", "subagent", "other_shape"} {
		message := observation(t, frames[name])
		require.Nil(t, message.ToolPreview, name)
		require.NotEmpty(t, message.Detail, name)
	}
	messages, _, err := claudecode.UpstreamToMessages(context.Background(), "work", frames["two_results"])
	require.NoError(t, err)
	require.Len(t, messages, 2)
	for _, message := range messages {
		require.Nil(t, message.ToolPreview, "a result shared by several tool_results belongs to none")
	}
}

func TestToolPreviewIsCutAtItsLimit(t *testing.T) {
	const limit = 256 << 10
	line := strings.Repeat("字", 99) + "\n"
	body := strings.Repeat(line, limit/100+10)
	frame, _ := json.Marshal(map[string]any{
		"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "w", "content": "ok"}}},
		"tool_use_result": map[string]any{"type": "update", "filePath": "/workspace/long.txt", "content": body},
	})
	preview := observation(t, frame).ToolPreview
	require.True(t, preview.Truncated)
	require.Equal(t, uint32(len([]rune(body))), preview.TotalLength)
	require.LessOrEqual(t, len([]rune(preview.Content)), limit)
	require.True(t, strings.HasSuffix(preview.Content, "\n"), "cut at a line break")
	require.True(t, strings.HasPrefix(body, preview.Content))

	hunk := map[string]any{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []string{"+" + strings.Repeat("x", 100<<10)}}
	frame, _ = json.Marshal(map[string]any{
		"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "e", "content": "ok"}}},
		"tool_use_result": map[string]any{"filePath": "/workspace/a.txt", "oldString": "a", "newString": "b", "structuredPatch": []any{hunk, hunk, hunk}},
	})
	edit := observation(t, frame).ToolPreview
	require.True(t, edit.Truncated)
	require.Len(t, edit.Hunks, 2, "hunks are cut whole")

	huge := map[string]any{"oldStart": 1, "oldLines": 1, "newStart": 1, "newLines": 1, "lines": []string{"+" + strings.Repeat("x", 300<<10)}}
	frame, _ = json.Marshal(map[string]any{
		"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "e", "content": "ok"}}},
		"tool_use_result": map[string]any{"filePath": "/workspace/a.txt", "oldString": "a", "newString": "b", "structuredPatch": []any{huge}},
	})
	require.Nil(t, observation(t, frame).ToolPreview, "with no whole hunk to show, only the original output remains")
}

func stripped(preview *dispatcherpublicv1.ToolOutputPreview) *dispatcherpublicv1.ToolOutputPreview {
	return &dispatcherpublicv1.ToolOutputPreview{
		ToolName: preview.ToolName, FilePath: preview.FilePath, Content: preview.Content, StartLine: preview.StartLine,
		NumLines: preview.NumLines, TotalLines: preview.TotalLines, TruncatedByTokenCap: preview.TruncatedByTokenCap, TotalLength: preview.TotalLength,
	}
}

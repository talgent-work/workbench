//go:build integration

package unifiedmessagelog_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
	dispatcherpublicv1 "github.com/talgent-work/workbench/gen/go/talgent/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/adapter/claudecode"
	"github.com/talgent-work/workbench/internal/business/dispatcher/infrastructure/unifiedmessagelog"
	sgquery "github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	sglog "github.com/talgent-work/workbench/internal/business/streamgateway/infrastructure/unifiedmessagelog"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestOriginalDetailsRemainSeparateBoundedAndWorkScoped(t *testing.T) {
	ctx, dsn, cleanup := startClickHouse(t)
	defer cleanup()
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	writer, reader := unifiedmessagelog.NewWriter(conn), sglog.NewClickHouseReader(conn)
	text := strings.Repeat("原文🙂 e\u0301\n", 100000)
	input := `{"file_path":"/work/report.md","content":"` + strings.ReplaceAll(text, "\n", `\n`) + `"}`
	encodedOutput, err := json.Marshal(text)
	require.NoError(t, err)
	frames := map[string]string{
		"input":  `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool","name":"Write","input":` + input + `}]}}`,
		"output": `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool","content":` + string(encodedOutput) + `}]}}`,
		"empty":  `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"empty-tool","content":""}]}}`,
		"none":   `{"type":"assistant","message":{"content":[{"type":"text","text":"Done"}]}}`,
	}
	for id, raw := range frames {
		projections, _, err := claudecode.UpstreamToMessages(ctx, "work", []byte(raw))
		require.NoError(t, err)
		require.Len(t, projections, 1)
		projections[0].Id, projections[0].Timestamp = id, timestamppb.New(time.Now())
		require.NoError(t, writer.Append(ctx, projections[0]))
	}

	require.Eventually(t, func() bool {
		rows, err := reader.ListMessagesBySession(ctx, sgquery.ListMessagesBySessionRequest{AgentWorkID: "work"})
		return err == nil && len(rows) == len(frames)
	}, 5*time.Second, 50*time.Millisecond)
	for _, cursor := range []sgquery.ReplayCursor{{Latest: true}, {Limit: 500}} {
		rows, err := reader.ListReplay(ctx, "work", cursor)
		require.NoError(t, err)
		require.Len(t, rows, len(frames))
		for _, m := range rows {
			encoded, err := json.Marshal(m)
			require.NoError(t, err)
			require.Less(t, len(encoded), 1024, "replay/list must never deserialize full detail")
		}
	}
	for id, original := range map[string]string{"input": input, "output": text} {
		var recovered strings.Builder
		var offset uint32
		for {
			detail, err := reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "work", MessageID: id, Offset: offset, Limit: 16381})
			require.NoError(t, err)
			require.True(t, utf8.ValidString(detail.Text))
			require.LessOrEqual(t, utf8.RuneCountInString(detail.Text), 16381)
			require.EqualValues(t, utf8.RuneCountInString(original), detail.TotalLength)
			recovered.WriteString(detail.Text)
			if detail.NextOffset == detail.TotalLength {
				break
			}
			require.Greater(t, detail.NextOffset, offset)
			offset = detail.NextOffset
		}
		require.Equal(t, original, recovered.String())
	}
	combined, err := reader.GetToolDetail(ctx, sgquery.ToolDetailRequest{AgentWorkID: "work", ToolCallID: "tool", InputOffset: 3, OutputOffset: 5, Limit: 11})
	require.NoError(t, err)
	require.NotNil(t, combined.Input)
	require.NotNil(t, combined.Output)
	require.Equal(t, string([]rune(input)[3:14]), combined.Input.Text)
	require.Equal(t, string([]rune(text)[5:16]), combined.Output.Text)
	require.EqualValues(t, 14, combined.Input.NextOffset)
	require.EqualValues(t, 16, combined.Output.NextOffset)
	missing, err := reader.GetToolDetail(ctx, sgquery.ToolDetailRequest{AgentWorkID: "other-work", ToolCallID: "tool", Limit: 10})
	require.NoError(t, err)
	require.Nil(t, missing.Input)
	require.Nil(t, missing.Output)
	emptyTool, err := reader.GetToolDetail(ctx, sgquery.ToolDetailRequest{AgentWorkID: "work", ToolCallID: "empty-tool", Limit: 10})
	require.NoError(t, err)
	require.Nil(t, emptyTool.Input)
	require.NotNil(t, emptyTool.Output)
	require.EqualValues(t, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, emptyTool.Output.Format)
	require.Empty(t, emptyTool.Output.Text)
	empty, err := reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "work", MessageID: "empty", Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, dispatcherpublicv1.MessageDetailFormat_MESSAGE_DETAIL_FORMAT_TEXT, empty.Format)
	require.Zero(t, empty.TotalLength)
	require.Empty(t, empty.Text)
	none, err := reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "work", MessageID: "none", Limit: 10})
	require.NoError(t, err)
	require.Zero(t, none.Format)
	_, err = reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "other-work", MessageID: "output", Limit: 10})
	require.ErrorIs(t, err, sgquery.ErrMessageNotFound)
	_, err = reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "work", MessageID: "not-stored-yet", Limit: 10})
	require.ErrorIs(t, err, sgquery.ErrMessageNotFound)
	past, err := reader.GetMessageDetail(ctx, sgquery.MessageDetailRequest{AgentWorkID: "work", MessageID: "output", Offset: ^uint32(0), Limit: 10})
	require.NoError(t, err)
	require.Empty(t, past.Text)
	require.Equal(t, past.TotalLength, past.NextOffset)
}

// A Write's preview is stored with its result, kept out of history, and read
// with the call's details; the Action's tool name decides whether it shows.
func TestToolPreviewIsStoredWithTheResultAndReadWithItsDetail(t *testing.T) {
	ctx, dsn, cleanup := startClickHouse(t)
	defer cleanup()
	opts, err := clickhouse.ParseDSN(dsn)
	require.NoError(t, err)
	conn, err := clickhouse.Open(opts)
	require.NoError(t, err)
	defer conn.Close()
	writer, reader := unifiedmessagelog.NewWriter(conn), sglog.NewClickHouseReader(conn)
	frames := map[string]string{
		"call":   `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"write-1","name":"Write","input":{"file_path":"/workspace/r.md","content":"INPUT"}}]}}`,
		"result": `{"type":"user","parent_tool_use_id":null,"message":{"content":[{"type":"tool_result","tool_use_id":"write-1","content":"File created successfully at: /workspace/r.md"}]},"tool_use_result":{"type":"create","filePath":"/workspace/r.md","content":"# Written","structuredPatch":[],"originalFile":null}}`,
	}
	for id, raw := range frames {
		projections, _, err := claudecode.UpstreamToMessages(ctx, "preview-work", []byte(raw))
		require.NoError(t, err)
		require.Len(t, projections, 1)
		projections[0].Id, projections[0].Timestamp = id, timestamppb.New(time.Now())
		require.NoError(t, writer.Append(ctx, projections[0]))
	}
	require.Eventually(t, func() bool {
		detail, err := reader.GetToolDetail(ctx, sgquery.ToolDetailRequest{AgentWorkID: "preview-work", ToolCallID: "write-1", Limit: 100})
		return err == nil && detail.Preview != nil && detail.Input != nil
	}, 5*time.Second, 50*time.Millisecond)
	detail, err := reader.GetToolDetail(ctx, sgquery.ToolDetailRequest{AgentWorkID: "preview-work", ToolCallID: "write-1", Limit: 100})
	require.NoError(t, err)
	require.Equal(t, "Write", detail.ToolName)
	require.Equal(t, &sgquery.ToolOutputPreview{ToolName: "Write", FilePath: "/workspace/r.md", Content: "# Written", TotalLength: 9}, detail.Preview)
	require.Equal(t, "File created successfully at: /workspace/r.md", detail.Output.Text)

	rows, err := reader.ListMessagesBySession(ctx, sgquery.ListMessagesBySessionRequest{AgentWorkID: "preview-work"})
	require.NoError(t, err)
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "# Written", "history never carries the preview")
	}
}

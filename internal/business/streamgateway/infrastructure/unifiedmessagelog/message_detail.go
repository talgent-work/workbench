package unifiedmessagelog

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"unicode/utf8"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/streamgateway/application/query"
	"google.golang.org/protobuf/encoding/protojson"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
)

func (r *ClickHouseReader) GetMessageDetail(ctx context.Context, request query.MessageDetailRequest) (query.MessageDetail, error) {
	qctx := clickhousego.Context(ctx, clickhousego.WithParameters(clickhousego.Parameters{
		"agent_work_id": request.AgentWorkID, "id": request.MessageID,
		"detail_offset": strconv.FormatUint(uint64(request.Offset)+1, 10), "detail_limit": strconv.FormatUint(uint64(request.Limit), 10),
	}))
	var detail query.MessageDetail
	err := r.conn.QueryRow(qctx, `SELECT detail_format, detail_length,
		substringUTF8(detail_body, {detail_offset:UInt64}, {detail_limit:UInt32})
		FROM talgent_messages.unified_message FINAL
		WHERE agent_work_id = {agent_work_id:String} AND id = {id:String}`,
	).Scan(&detail.Format, &detail.TotalLength, &detail.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return detail, query.ErrMessageNotFound
	}
	if err != nil {
		return detail, oops.With("operation", "message.detail").Wrap(err)
	}
	detail.NextOffset = min(request.Offset, detail.TotalLength) + uint32(utf8.RuneCountInString(detail.Text))
	return detail, nil
}

func (r *LazyClickHouseReader) GetMessageDetail(ctx context.Context, request query.MessageDetailRequest) (query.MessageDetail, error) {
	conn, err := r.client.Conn(ctx)
	if err != nil {
		return query.MessageDetail{}, err
	}
	return NewClickHouseReader(conn).GetMessageDetail(ctx, request)
}

func (r *ClickHouseReader) GetToolDetail(ctx context.Context, request query.ToolDetailRequest) (query.ToolDetail, error) {
	qctx := clickhousego.Context(ctx, clickhousego.WithParameters(clickhousego.Parameters{
		"agent_work_id": request.AgentWorkID, "tool_call_id": request.ToolCallID,
		"input_offset":  strconv.FormatUint(uint64(request.InputOffset)+1, 10),
		"output_offset": strconv.FormatUint(uint64(request.OutputOffset)+1, 10),
		"detail_limit":  strconv.FormatUint(uint64(request.Limit), 10),
	}))
	rows, err := r.conn.Query(qctx, `SELECT kind, detail_format, detail_length,
 substringUTF8(detail_body, if(kind = 'UNIFIED_MESSAGE_KIND_ACTION', {input_offset:UInt64}, {output_offset:UInt64}), {detail_limit:UInt32}),
 if(kind = 'UNIFIED_MESSAGE_KIND_ACTION', JSONExtractString(payload_json, 'action', 'tool_name'), ''),
 if(kind = 'UNIFIED_MESSAGE_KIND_OBSERVATION', tool_preview, '')
 FROM talgent_messages.unified_message FINAL
 WHERE agent_work_id = {agent_work_id:String} AND tool_call_id = {tool_call_id:String}
 AND kind IN ('UNIFIED_MESSAGE_KIND_ACTION', 'UNIFIED_MESSAGE_KIND_OBSERVATION')
 ORDER BY timestamp DESC, id DESC LIMIT 1 BY kind`)
	if err != nil {
		return query.ToolDetail{}, oops.With("operation", "tool.detail").Wrap(err)
	}
	defer rows.Close()
	var result query.ToolDetail
	for rows.Next() {
		var kind, toolName, previewJSON string
		detail := &query.MessageDetail{}
		if err := rows.Scan(&kind, &detail.Format, &detail.TotalLength, &detail.Text, &toolName, &previewJSON); err != nil {
			return query.ToolDetail{}, err
		}
		offset := request.OutputOffset
		if kind == "UNIFIED_MESSAGE_KIND_ACTION" {
			result.Input, result.ToolName, offset = detail, toolName, request.InputOffset
		} else {
			preview, err := toolPreviewFromJSON(previewJSON)
			if err != nil {
				return query.ToolDetail{}, oops.With("operation", "tool.detail.preview").Wrap(err)
			}
			result.Output, result.Preview = detail, preview
		}
		detail.NextOffset = min(offset, detail.TotalLength) + uint32(utf8.RuneCountInString(detail.Text))
	}
	return result, rows.Err()
}

func (r *LazyClickHouseReader) GetToolDetail(ctx context.Context, request query.ToolDetailRequest) (query.ToolDetail, error) {
	conn, err := r.client.Conn(ctx)
	if err != nil {
		return query.ToolDetail{}, err
	}
	return NewClickHouseReader(conn).GetToolDetail(ctx, request)
}

// toolPreviewFromJSON reads a stored preview; an observation without one
// shows the original output only.
func toolPreviewFromJSON(raw string) (*query.ToolOutputPreview, error) {
	if raw == "" {
		return nil, nil
	}
	var stored dispatcherpublicv1.ToolOutputPreview
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal([]byte(raw), &stored); err != nil {
		return nil, err
	}
	preview := &query.ToolOutputPreview{
		ToolName: stored.GetToolName(), FilePath: stored.GetFilePath(), Content: stored.GetContent(),
		StartLine: stored.GetStartLine(), NumLines: stored.GetNumLines(), TotalLines: stored.GetTotalLines(),
		TruncatedByTokenCap: stored.GetTruncatedByTokenCap(), Truncated: stored.GetTruncated(), TotalLength: stored.GetTotalLength(),
	}
	for _, hunk := range stored.GetHunks() {
		preview.Hunks = append(preview.Hunks, query.PatchHunk{
			OldStart: hunk.GetOldStart(), OldLines: hunk.GetOldLines(), NewStart: hunk.GetNewStart(), NewLines: hunk.GetNewLines(),
			Lines: append([]string(nil), hunk.GetLines()...),
		})
	}
	return preview, nil
}

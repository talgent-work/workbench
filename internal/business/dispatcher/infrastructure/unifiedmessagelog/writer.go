package unifiedmessagelog

import (
	"context"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/dispatcher/public/v1"
)

// Writer appends UnifiedMessages, their details included, to the message log.
// The caller batches; an append returns once ClickHouse has stored the batch.
type Writer struct {
	conn driver.Conn
}

func NewWriter(conn driver.Conn) *Writer {
	return &Writer{conn: conn}
}

func (w *Writer) Append(ctx context.Context, messages ...*dispatcherpublicv1.UnifiedMessage) error {
	if len(messages) == 0 {
		return nil
	}
	batch, err := w.conn.PrepareBatch(ctx, insertStmt)
	if err != nil {
		return oops.With("agent_work_id", messages[0].GetAgentWorkId()).Wrap(err)
	}
	for _, msg := range messages {
		do, err := messageToDO(msg)
		if err != nil {
			return oops.Wrap(err)
		}
		if err := batch.Append(
			do.ID, do.AgentWorkID, do.TurnID, do.Agent, do.Kind, do.ToolCallID, do.InteractionID, do.PayloadJSON, do.Timestamp,
			uint8(msg.GetDetailFormat()), msg.GetDetailLength(), msg.GetDetail(), do.ToolPreviewJSON,
		); err != nil {
			return oops.With("agent_work_id", do.AgentWorkID, "id", do.ID).Wrap(err)
		}
	}
	if err := batch.Send(); err != nil {
		return oops.With("agent_work_id", messages[0].GetAgentWorkId()).Wrap(err)
	}
	return nil
}

const insertColumns = `id, agent_work_id, turn_id, agent, kind, tool_call_id, interaction_id, payload_json, timestamp, detail_format, detail_length, detail_body, tool_preview`

const insertStmt = `INSERT INTO ` + Table + ` (` + insertColumns + `)`

package turnresults

import (
	"context"
	"strconv"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// stepWindow bounds the root messages read from the call's result onwards;
// a step's results and the message after them fit well within it.
const stepWindow = 500

// The root stream from the call's result onwards, read from protojson
// payloads written with proto field names. FINAL collapses replayed versions
// of one message.
const callStepQuery = `SELECT kind, tool_call_id, JSONExtractString(payload_json, 'observation', 'context_position')
FROM talgent_messages.unified_message FINAL
WHERE agent_work_id = {work_id:String} AND agent = ''
  AND timestamp >= (
    SELECT min(timestamp) FROM talgent_messages.unified_message FINAL
    WHERE agent_work_id = {work_id:String} AND agent = '' AND kind = 'UNIFIED_MESSAGE_KIND_OBSERVATION' AND tool_call_id = {call_id:String})
ORDER BY timestamp, id
LIMIT {window:UInt32}`

const (
	observationKind = "UNIFIED_MESSAGE_KIND_OBSERVATION"
	assistantKind   = "UNIFIED_MESSAGE_KIND_ASSISTANT"
	resultKind      = "UNIFIED_MESSAGE_KIND_RESULT"
)

var _ query.CallPositionReader = (*Reader)(nil)

// FindCallPosition names where the step holding the call returned in the
// Work's root stream: the last result of that step's calls. The model calls
// tools in parallel and their results arrive one by one, so a position is
// named only once the Harness moved past the step with an assistant message
// or a Turn result; until then the call has not returned as far as a
// derived Work is concerned, and no fork lands inside an unfinished call.
func (r *Reader) FindCallPosition(ctx context.Context, workID, callID string) (string, bool, error) {
	conn, err := r.conn.Conn(ctx)
	if err != nil {
		return "", false, oops.With("operation", "call_position.connect").Wrap(err)
	}
	queryCtx := clickhousego.Context(ctx, clickhousego.WithParameters(clickhousego.Parameters{"work_id": workID, "call_id": callID, "window": strconv.Itoa(stepWindow)}))
	rows, err := conn.Query(queryCtx, callStepQuery)
	if err != nil {
		return "", false, oops.With("operation", "call_position.find").With("work_id", workID).Wrap(err)
	}
	defer rows.Close()
	var steps []stepMessage
	for rows.Next() {
		var message stepMessage
		if err := rows.Scan(&message.kind, &message.callID, &message.position); err != nil {
			return "", false, oops.With("operation", "call_position.scan").With("work_id", workID).Wrap(err)
		}
		steps = append(steps, message)
	}
	if err := rows.Err(); err != nil {
		return "", false, oops.With("operation", "call_position.find").With("work_id", workID).Wrap(err)
	}
	position, returned := stepPosition(callID, steps)
	return position, returned, nil
}

type stepMessage struct{ kind, callID, position string }

// stepPosition reads the root messages from the call's result onwards: the
// results that follow it belong to the same step until the Harness moves on.
// Messages before the call's own result, which the read may include when no
// such result exists, are skipped.
func stepPosition(callID string, messages []stepMessage) (string, bool) {
	position, reached := "", false
	for _, message := range messages {
		if !reached {
			reached = message.kind == observationKind && message.callID == callID
			if !reached {
				continue
			}
		}
		switch message.kind {
		case observationKind:
			if message.position != "" {
				position = message.position
			}
		case assistantKind, resultKind:
			return position, position != ""
		}
	}
	return "", false
}

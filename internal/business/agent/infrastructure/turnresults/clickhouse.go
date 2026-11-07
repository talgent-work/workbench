// Package turnresults reads the turn results of a Work's root message stream
// from the durable message log the Dispatcher writes.
package turnresults

import (
	"context"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"
	"google.golang.org/protobuf/encoding/protojson"

	dispatcherpublicv1 "github.com/talgent-work/contracts/gen/go/dispatcher/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	pkgclickhouse "github.com/talgent-work/workbench/internal/pkg/clickhouse"
)

// The root stream's own messages carry an empty agent; a subagent's carry its
// name. FINAL collapses replayed versions of one message.
const rootResultsQuery = `SELECT id, payload_json, timestamp
FROM talgent_messages.unified_message FINAL
WHERE agent_work_id = {work_id:String} AND kind = 'UNIFIED_MESSAGE_KIND_RESULT' AND agent = ''
ORDER BY timestamp, id`

type connection interface {
	Conn(ctx context.Context) (driver.Conn, error)
}

// Reader implements query.RootTurnResultReader over ClickHouse.
type Reader struct {
	conn connection
}

var _ query.RootTurnResultReader = (*Reader)(nil)

func NewReader(conn *pkgclickhouse.LazyConn) *Reader {
	return &Reader{conn: conn}
}

var payloadJSON = protojson.UnmarshalOptions{DiscardUnknown: true}

func (r *Reader) ListRootTurnResults(ctx context.Context, workID string) ([]query.RootTurnResult, error) {
	conn, err := r.conn.Conn(ctx)
	if err != nil {
		return nil, oops.With("operation", "turn_results.connect").Wrap(err)
	}
	queryCtx := clickhousego.Context(ctx, clickhousego.WithParameters(clickhousego.Parameters{"work_id": workID}))
	rows, err := conn.Query(queryCtx, rootResultsQuery)
	if err != nil {
		return nil, oops.With("operation", "turn_results.list").With("work_id", workID).Wrap(err)
	}
	defer rows.Close()
	var results []query.RootTurnResult
	for rows.Next() {
		var (
			id, payload string
			occurredAt  time.Time
		)
		if err := rows.Scan(&id, &payload, &occurredAt); err != nil {
			return nil, oops.With("operation", "turn_results.scan").With("work_id", workID).Wrap(err)
		}
		message := &dispatcherpublicv1.UnifiedMessage{}
		if err := payloadJSON.Unmarshal([]byte(payload), message); err != nil {
			return nil, oops.With("operation", "turn_results.decode").With("work_id", workID).With("message_id", id).Wrap(err)
		}
		results = append(results, query.RootTurnResult{MessageID: id, Text: message.GetResult().GetText(), OccurredAt: occurredAt.UTC()})
	}
	if err := rows.Err(); err != nil {
		return nil, oops.With("operation", "turn_results.list").With("work_id", workID).Wrap(err)
	}
	return results, nil
}

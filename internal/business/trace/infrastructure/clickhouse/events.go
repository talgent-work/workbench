package clickhouse

import (
	"context"
	"fmt"
	"strings"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

var _ query.EventRepository = (*Repository)(nil)

// Keep the evidence identity identical to trace_logs_measurements_mv. It includes the
// native sequence and timestamp; identical OTLP retries therefore share an ID.
const logEvidenceID = `talgent_log_evidence_id(TraceId, Timestamp, Body, LogAttributes)`

func (r *Repository) ListEvents(ctx context.Context, opts query.EventOptions) (*query.EventResult, error) {
	params := clickhousego.Parameters{"project_id": opts.ProjectID}
	filters := []string{"ResourceAttributes['talgent.project_id'] = {project_id:String}", "Timestamp >= now64(9) - INTERVAL 7 DAY"}
	for _, f := range []struct{ value, name, column string }{
		{opts.AgentWorkID, "session_id", "ResourceAttributes['talgent.agent_work_id']"},
		{opts.TraceID, "trace_id", "TraceId"}, {opts.CallID, "call_id", "LogAttributes['tool_use_id']"},
	} {
		if f.value != "" {
			params[f.name] = f.value
			filters = append(filters, f.column+" = {"+f.name+":String}")
		}
	}
	if !opts.StartTime.IsZero() {
		params["start_time"] = opts.StartTime.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "Timestamp >= {start_time:DateTime64(9, 'UTC')}")
	}
	if !opts.EndTime.IsZero() {
		params["end_time"] = opts.EndTime.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "Timestamp < {end_time:DateTime64(9, 'UTC')}")
	}
	if opts.AfterCursor != "" {
		at, id, err := decodeEvidenceCursor(opts.AfterCursor)
		if err != nil {
			return nil, err
		}
		params["after_time"] = at.UTC().Format("2006-01-02 15:04:05.999999999")
		params["after_id"] = id
		filters = append(filters, "(Timestamp, evidence_id) > ({after_time:DateTime64(9, 'UTC')}, {after_id:String})")
	}
	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	statement := fmt.Sprintf(`SELECT DISTINCT %s AS evidence_id, Timestamp, TraceId, SpanId, Body, mapSort(LogAttributes), mapSort(ResourceAttributes)
 FROM otel_logs WHERE %s ORDER BY Timestamp, evidence_id LIMIT %d`, logEvidenceID, strings.Join(filters, " AND "), limit+1)
	rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), statement)
	if err != nil {
		return nil, oops.With("operation", "trace.events").Wrap(err)
	}
	defer rows.Close()
	result := &query.EventResult{}
	for rows.Next() {
		var e query.NativeEvent
		if err := rows.Scan(&e.ID, &e.Timestamp, &e.TraceID, &e.SpanID, &e.Body, &e.Attributes, &e.ResourceAttributes); err != nil {
			return nil, oops.With("operation", "trace.events").Wrap(err)
		}
		e.Name = e.Attributes["event.name"]
		e.ProjectID = e.ResourceAttributes["talgent.project_id"]
		e.AgentWorkID = e.ResourceAttributes["talgent.agent_work_id"]
		result.Events = append(result.Events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.With("operation", "trace.events").Wrap(err)
	}
	if len(result.Events) > limit {
		last := result.Events[limit-1]
		result.NextCursor, _ = encodeEvidenceCursor(last.Timestamp, last.ID)
		result.Events = result.Events[:limit]
	}
	return result, nil
}

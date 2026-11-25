package clickhouse

import (
	"context"
	"fmt"
	"strings"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/samber/oops"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// eventLimit caps an event page.
const eventLimit = 500

// ListEvents reads a Work Caller's interaction events oldest first, in the
// table's sort key order after the Work Caller.
func (r *Repository) ListEvents(ctx context.Context, opts query.EventOptions) (*query.EventResult, error) {
	limit := opts.Limit
	if limit <= 0 || limit > eventLimit {
		limit = eventLimit
	}
	events, next, err := readEvidencePage(ctx, opts.AfterCursor, limit, func(ctx context.Context, after string) ([]listedEvent, error) {
		opts.AfterCursor = after
		statement, params, err := buildEventListQuery(opts, limit)
		if err != nil {
			return nil, err
		}
		return r.readEvents(ctx, statement, params)
	})
	if err != nil {
		return nil, err
	}
	result := &query.EventResult{Events: make([]query.NativeEvent, len(events)), NextCursor: next}
	for i, event := range events {
		result.Events[i] = event.NativeEvent
	}
	return result, nil
}

func buildEventListQuery(opts query.EventOptions, limit int) (string, clickhousego.Parameters, error) {
	// Every scope names its Work Caller; the empty one names no Work.
	params := clickhousego.Parameters{"work_caller": opts.WorkCaller.String()}
	filters := []string{"WorkCaller = {work_caller:String}", "WorkCaller != ''", "Timestamp >= now64(9) - INTERVAL 7 DAY"}
	for _, f := range []struct{ value, name, column string }{
		{opts.AgentWorkID, "session_id", "AgentWorkId"},
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
		after, err := afterCursorFilter(opts.AfterCursor, false, "SessionId", "EventSequence", params)
		if err != nil {
			return "", nil, err
		}
		filters = append(filters, after...)
	}
	// Keep the evidence identity identical to the event measurement views.
	return fmt.Sprintf(`SELECT talgent_event_evidence_id(SessionId, EventSequence, Timestamp), SessionId, EventSequence,
 Timestamp, TraceId, SpanId, Body, LogAttributes, ResourceAttributes
 FROM native_events WHERE %s ORDER BY Timestamp, SessionId, EventSequence LIMIT %d`, strings.Join(filters, " AND "), limit+1), params, nil
}

func (r *Repository) readEvents(ctx context.Context, statement string, params clickhousego.Parameters) ([]listedEvent, error) {
	rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), statement)
	if err != nil {
		return nil, oops.With("operation", "trace.events").Wrap(err)
	}
	defer rows.Close()
	var read []listedEvent
	for rows.Next() {
		var e listedEvent
		var session, sequence string
		if err := rows.Scan(&e.ID, &session, &sequence, &e.Timestamp, &e.TraceID, &e.SpanID, &e.Body, &e.Attributes, &e.ResourceAttributes); err != nil {
			return nil, oops.With("operation", "trace.events").Wrap(err)
		}
		e.Name = e.Attributes["event.name"]
		e.WorkCaller = query.ParseWorkCaller(e.ResourceAttributes["talgent.work_caller"])
		e.WorkKey = query.ParseWorkKey(e.ResourceAttributes["talgent.work_key"])
		e.AgentWorkID = e.ResourceAttributes["talgent.agent_work_id"]
		if e.next, err = encodeEvidenceCursor(e.Timestamp, session, sequence); err != nil {
			return nil, oops.With("operation", "trace.events").Wrap(err)
		}
		read = append(read, e)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.With("operation", "trace.events").Wrap(err)
	}
	return read, nil
}

// listedEvent is an event a page read returns, with the cursor that resumes
// after it.
type listedEvent struct {
	query.NativeEvent
	next string
}

func (e listedEvent) identity() string { return e.ID }
func (e listedEvent) cursor() string   { return e.next }

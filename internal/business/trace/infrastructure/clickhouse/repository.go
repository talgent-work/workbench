// Package clickhouse implements Observability's NativeEvidenceQueryRepository
// over the native tables otel-collector-contrib's clickhouseexporter writes,
// and its MeasurementQueryRepository over the measurements derived from them.
package clickhouse

import (
	"context"
	"fmt"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

const defaultLimit = 1000

type Repository struct {
	db driver.Conn
}

var _ query.NativeEvidenceQueryRepository = (*Repository)(nil)
var _ query.MeasurementQueryRepository = (*Repository)(nil)

// NewRepository shares the Runtime-owned ClickHouse connection across reads;
// one Repository serves native evidence and derived measurements.
func NewRepository(db driver.Conn) *Repository { return &Repository{db: db} }

// ListBySession lists one Work's spans within its Work Caller.
func (r *Repository) ListBySession(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	return r.listSpans(ctx, spanScopeSession, opts)
}

// ListByWorkCaller lists a Work Caller's spans, narrowed by the trace
// attributes opts names and to opts.WorkIDs when not nil.
func (r *Repository) ListByWorkCaller(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	opts.OwnerContext = strings.TrimSpace(opts.OwnerContext)
	opts.OwnerID = strings.TrimSpace(opts.OwnerID)
	opts.RuntimePurpose = strings.TrimSpace(opts.RuntimePurpose)
	return r.listSpans(ctx, spanScopeWorkCaller, opts)
}

type spanListScope string

const (
	spanScopeSession    spanListScope = "session"
	spanScopeWorkCaller spanListScope = "work_caller"
)

func (r *Repository) listSpans(ctx context.Context, scope spanListScope, opts query.SpanListOptions) (*query.SpanListResult, error) {
	limit := opts.Limit
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}
	spans, next, err := readEvidencePage(ctx, opts.AfterCursor, limit, func(ctx context.Context, after string) ([]listedSpan, error) {
		opts.AfterCursor = after
		q, params, err := buildSpanListQuery(scope, opts)
		if err != nil {
			return nil, err
		}
		rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), q)
		if err != nil {
			return nil, oops.With("operation", "trace.query").Wrap(err)
		}
		defer rows.Close()
		read, err := scanSpans(rows)
		if err != nil {
			return nil, oops.With("operation", "trace.scan").Wrap(err)
		}
		listed := make([]listedSpan, len(read))
		for i, span := range read {
			listed[i] = listedSpan{span}
		}
		return listed, nil
	})
	if err != nil {
		return nil, err
	}
	result := &query.SpanListResult{Spans: make([]*query.Span, len(spans)), NextCursor: next}
	for i, span := range spans {
		result.Spans[i] = span.Span
	}
	return result, nil
}

// listedSpan is a span a page read returns.
type listedSpan struct{ *query.Span }

func (s listedSpan) identity() string { return s.TraceID + ":" + s.SpanID }
func (s listedSpan) cursor() string   { return s.Cursor }

// spanColumns are what a span read returns, in scanSpans order.
const spanColumns = `SpanId, TraceId, ParentSpanId, AgentWorkId,
			SpanName, SpanKind,
			Timestamp, Timestamp + INTERVAL Duration NANOSECOND,
			toInt64(Duration),
			StatusCode, StatusMessage,
			SpanAttributes, ResourceAttributes,
			talgent_span_category(SpanName, SpanAttributes)`

// buildSpanListQuery reads a page in the table's sort key order, after the
// Work Caller: one more span than the page holds tells whether more follow.
func buildSpanListQuery(scope spanListScope, opts query.SpanListOptions) (string, clickhousego.Parameters, error) {
	limit := opts.Limit
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}

	direction := "ASC"
	if opts.NewestFirst {
		direction = "DESC"
	}
	// Every scope names its Work Caller; the empty one names no Work.
	params := clickhousego.Parameters{"work_caller": opts.WorkCaller.String()}
	filters := []string{"WorkCaller = {work_caller:String}", "WorkCaller != ''", "Timestamp >= now64(9) - INTERVAL 7 DAY"}
	switch scope {
	case spanScopeWorkCaller:
		for _, attribute := range []struct{ name, value string }{
			{"owner_context", opts.OwnerContext}, {"owner_id", opts.OwnerID}, {"runtime_purpose", opts.RuntimePurpose},
		} {
			if value := strings.TrimSpace(attribute.value); value != "" {
				params[attribute.name] = value
				filters = append(filters, fmt.Sprintf("ResourceAttributes['talgent.%[1]s'] = {%[1]s:String}", attribute.name))
			}
		}
		if opts.WorkIDs != nil {
			params["work_ids"] = stringArrayParam(opts.WorkIDs)
			filters = append(filters, "AgentWorkId IN {work_ids:Array(String)}")
		}
	default:
		params["agent_work_id"] = strings.TrimSpace(opts.AgentWorkID)
		filters = append(filters, "AgentWorkId = {agent_work_id:String}")
	}
	if opts.AfterCursor != "" {
		after, err := afterCursorFilter(opts.AfterCursor, opts.NewestFirst, "TraceId", "SpanId", params)
		if err != nil {
			return "", nil, err
		}
		filters = append(filters, after...)
	}

	q := fmt.Sprintf(`
		SELECT %s
		FROM native_spans
		WHERE %s
		ORDER BY Timestamp %[3]s, TraceId %[3]s, SpanId %[3]s
		LIMIT %[4]d`, spanColumns, strings.Join(filters, "\n\t\tAND "), direction, limit+1)
	return q, params, nil
}

func (r *Repository) GetByIdentity(ctx context.Context, identity query.SpanIdentity) (*query.Span, error) {
	const q = `
		SELECT ` + spanColumns + `
		FROM native_spans
		WHERE WorkCaller = ? AND WorkCaller != ''
		  AND TraceId = ? AND SpanId = ?
		  AND (? = '' OR AgentWorkId = ?)
		  AND Timestamp >= now64(9) - INTERVAL 7 DAY
		LIMIT 1`
	rows, err := r.db.Query(ctx, q, identity.WorkCaller.String(), identity.TraceID, identity.SpanID, identity.AgentWorkID, identity.AgentWorkID)
	if err != nil {
		return nil, oops.With("operation", "trace.query").Wrap(err)
	}
	defer rows.Close()
	spans, err := scanSpans(rows)
	if err != nil {
		return nil, oops.With("operation", "trace.scan").Wrap(err)
	}
	if len(spans) == 0 {
		return nil, nil
	}
	return spans[0], nil
}

func scanSpans(rows driver.Rows) ([]*query.Span, error) {
	var out []*query.Span
	for rows.Next() {
		var (
			sp        query.Span
			start     time.Time
			end       time.Time
			attrs     map[string]string
			resources map[string]string
			duration  int64
		)
		if err := rows.Scan(
			&sp.SpanID, &sp.TraceID, &sp.ParentSpanID,
			&sp.AgentWorkID,
			&sp.Name, &sp.Kind,
			&start, &end, &duration,
			&sp.StatusCode, &sp.StatusMessage,
			&attrs, &resources, &sp.Category,
		); err != nil {
			return nil, err
		}
		sp.StartTime = start
		sp.EndTime = end
		sp.DurationNanos = duration
		sp.Attributes = attrs
		sp.ResourceAttributes = resources
		sp.CallID = attrs["tool_use_id"]
		sp.NativeAgentID = attrs["agent_id"]
		sp.ParentNativeAgentID = attrs["parent_agent_id"]
		scope := spanScopeFromAttributes(resources)
		sp.WorkCaller = scope.WorkCaller
		sp.WorkKey = scope.WorkKey
		sp.AgentWorkID = scope.AgentWorkID
		sp.AgentID = scope.AgentID
		sp.RuntimePurpose = scope.RuntimePurpose
		sp.OwnerContext = scope.OwnerContext
		sp.OwnerID = scope.OwnerID
		if cursor, err := encodeEvidenceCursor(sp.StartTime, sp.TraceID, sp.SpanID); err == nil {
			sp.Cursor = cursor
		}
		out = append(out, &sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func spanScopeFromAttributes(attrs map[string]string) query.Span {
	return query.Span{
		WorkCaller:     query.ParseWorkCaller(attrs["talgent.work_caller"]),
		WorkKey:        query.ParseWorkKey(attrs["talgent.work_key"]),
		AgentWorkID:    attrs["talgent.agent_work_id"],
		AgentID:        attrs["talgent.agent_id"],
		RuntimePurpose: attrs["talgent.runtime_purpose"],
		OwnerContext:   attrs["talgent.owner_context"],
		OwnerID:        attrs["talgent.owner_id"],
	}
}

func normalizeFilterValues(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func stringArrayParam(values []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, value := range values {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('\'')
		b.WriteString(strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`))
		b.WriteByte('\'')
	}
	b.WriteByte(']')
	return b.String()
}

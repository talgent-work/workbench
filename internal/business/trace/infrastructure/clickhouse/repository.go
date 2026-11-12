// Package clickhouse implements Observability's NativeEvidenceQueryRepository
// over the native tables otel-collector-contrib's clickhouseexporter writes,
// and its MeasurementQueryRepository over the measurements derived from them.
package clickhouse

import (
	"context"
	"encoding/base64"
	"encoding/json"
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

func (r *Repository) ListBySession(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	return r.listSpans(ctx, spanScopeSession, opts)
}

func (r *Repository) ListByOwner(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	opts.OwnerContext = strings.TrimSpace(opts.OwnerContext)
	opts.OwnerID = strings.TrimSpace(opts.OwnerID)
	opts.RuntimePurpose = strings.TrimSpace(opts.RuntimePurpose)
	return r.listSpans(ctx, spanScopeOwner, opts)
}

// ListByApplication lists the spans of an Application's Works, narrowed to
// opts.WorkIDs when not nil.
func (r *Repository) ListByApplication(ctx context.Context, opts query.SpanListOptions) (*query.SpanListResult, error) {
	opts.ApplicationID = strings.TrimSpace(opts.ApplicationID)
	return r.listSpans(ctx, spanScopeApplication, opts)
}

type spanListScope string

const (
	spanScopeSession     spanListScope = "session"
	spanScopeOwner       spanListScope = "owner"
	spanScopeApplication spanListScope = "application"
)

func (r *Repository) listSpans(ctx context.Context, scope spanListScope, opts query.SpanListOptions) (*query.SpanListResult, error) {
	limit := opts.Limit
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}

	q, params, err := buildSpanListQuery(scope, opts)
	if err != nil {
		return nil, err
	}
	rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), q)
	if err != nil {
		return nil, oops.With("operation", "trace.query").Wrap(err)
	}
	defer rows.Close()

	spans, err := scanSpans(rows)
	if err != nil {
		return nil, oops.With("operation", "trace.scan").Wrap(err)
	}
	result := &query.SpanListResult{}
	if len(spans) > limit {
		result.NextCursor = spans[limit-1].Cursor
		spans = spans[:limit]
	}
	result.Spans = spans
	return result, nil
}

func buildSpanListQuery(scope spanListScope, opts query.SpanListOptions) (string, clickhousego.Parameters, error) {
	limit := opts.Limit
	if limit <= 0 || limit > defaultLimit {
		limit = defaultLimit
	}

	direction, beyond := "ASC", ">"
	if opts.NewestFirst {
		direction, beyond = "DESC", "<"
	}
	params := clickhousego.Parameters{}
	filters := []string{"Timestamp >= now64(9) - INTERVAL 7 DAY"}
	switch scope {
	case spanScopeOwner:
		params["project_id"] = strings.TrimSpace(opts.ProjectID)
		params["owner_context"] = strings.TrimSpace(opts.OwnerContext)
		params["owner_id"] = strings.TrimSpace(opts.OwnerID)
		filters = append(filters,
			"ResourceAttributes['talgent.project_id'] = {project_id:String}",
			"ResourceAttributes['talgent.owner_context'] = {owner_context:String}",
			"ResourceAttributes['talgent.owner_id'] = {owner_id:String}",
		)
		if runtimePurpose := strings.TrimSpace(opts.RuntimePurpose); runtimePurpose != "" {
			params["runtime_purpose"] = runtimePurpose
			filters = append(filters, "ResourceAttributes['talgent.runtime_purpose'] = {runtime_purpose:String}")
		}
	case spanScopeApplication:
		// An empty Application matches only Works no Application owns.
		params["application_id"] = strings.TrimSpace(opts.ApplicationID)
		filters = append(filters,
			"ResourceAttributes['talgent.application_id'] = {application_id:String}",
			"ResourceAttributes['talgent.application_id'] != ''",
			"ResourceAttributes['talgent.project_id'] = ''")
		if opts.WorkIDs != nil {
			params["work_ids"] = stringArrayParam(opts.WorkIDs)
			filters = append(filters, "has({work_ids:Array(String)}, ResourceAttributes['talgent.agent_work_id'])")
		}
	default:
		params["agent_work_id"] = strings.TrimSpace(opts.AgentWorkID)
		params["project_id"] = strings.TrimSpace(opts.ProjectID)
		filters = append(filters, "ResourceAttributes['talgent.project_id'] = {project_id:String}", "ResourceAttributes['talgent.agent_work_id'] = {agent_work_id:String}")
	}
	if opts.AfterCursor != "" {
		start, evidenceID, err := decodeEvidenceCursor(opts.AfterCursor)
		if err != nil {
			return "", nil, err
		}
		params["after_time"] = start.UTC().Format("2006-01-02 15:04:05.999999999")
		params["after_id"] = evidenceID
		filters = append(filters, fmt.Sprintf(`(Timestamp %[1]s {after_time:DateTime64(9, 'UTC')}
	OR (Timestamp = {after_time:DateTime64(9, 'UTC')} AND concat(TraceId, ':', SpanId) %[1]s {after_id:String}))`, beyond))
	}

	q := fmt.Sprintf(`
		SELECT DISTINCT
			SpanId, TraceId, ParentSpanId,
			ResourceAttributes['talgent.agent_work_id'],
			SpanName, SpanKind,
			Timestamp, Timestamp + INTERVAL Duration NANOSECOND,
			toInt64(Duration),
			StatusCode, StatusMessage,
			mapSort(SpanAttributes), mapSort(ResourceAttributes),
            talgent_span_category(SpanName, SpanAttributes)
		FROM otel_traces
		WHERE %s
		ORDER BY Timestamp %[2]s, concat(TraceId, ':', SpanId) %[2]s
		LIMIT %[3]d`, strings.Join(filters, "\n\t\tAND "), direction, limit+1)
	return q, params, nil
}

func (r *Repository) GetByIdentity(ctx context.Context, identity query.SpanIdentity) (*query.Span, error) {
	const q = `
		SELECT DISTINCT
			SpanId, TraceId, ParentSpanId,
			ResourceAttributes['talgent.agent_work_id'],
			SpanName, SpanKind,
			Timestamp, Timestamp + INTERVAL Duration NANOSECOND,
			toInt64(Duration),
			StatusCode, StatusMessage,
			mapSort(SpanAttributes), mapSort(ResourceAttributes),
            talgent_span_category(SpanName, SpanAttributes)
		FROM otel_traces
		WHERE ResourceAttributes['talgent.project_id'] = ? AND TraceId = ? AND SpanId = ?
		  AND (? = '' OR ResourceAttributes['talgent.agent_work_id'] = ?)
		  AND Timestamp >= now64(9) - INTERVAL 7 DAY
		LIMIT 1`
	rows, err := r.db.Query(ctx, q, identity.ProjectID, identity.TraceID, identity.SpanID, identity.AgentWorkID, identity.AgentWorkID)
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
		sp.ProjectID = scope.ProjectID
		sp.AgentWorkID = scope.AgentWorkID
		sp.AgentID = scope.AgentID
		sp.RuntimePurpose = scope.RuntimePurpose
		sp.OwnerContext = scope.OwnerContext
		sp.OwnerID = scope.OwnerID
		if cursor, err := encodeEvidenceCursor(sp.StartTime, sp.TraceID+":"+sp.SpanID); err == nil {
			sp.Cursor = cursor
		}
		out = append(out, &sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type evidenceCursor struct {
	StartTime string `json:"start_time"`
	ID        string `json:"id"`
}

func encodeEvidenceCursor(startTime time.Time, evidenceID string) (string, error) {
	payload, err := json.Marshal(evidenceCursor{
		StartTime: startTime.UTC().Format(time.RFC3339Nano),
		ID:        evidenceID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeEvidenceCursor(cursor string) (time.Time, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	var payload evidenceCursor
	if err := json.Unmarshal(raw, &payload); err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	start, err := time.Parse(time.RFC3339Nano, payload.StartTime)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%w: %v", query.ErrInvalidSpanCursor, err)
	}
	if payload.ID == "" {
		return time.Time{}, "", fmt.Errorf("%w: evidence id is required", query.ErrInvalidSpanCursor)
	}
	return start, payload.ID, nil
}

func spanScopeFromAttributes(attrs map[string]string) query.Span {
	return query.Span{
		ProjectID:      attrs["talgent.project_id"],
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

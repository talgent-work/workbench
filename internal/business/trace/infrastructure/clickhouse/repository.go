// Package clickhouse implements query.SpanQueryRepository by querying
// the otel_traces table written by otel-collector-contrib's clickhouseexporter.
package clickhouse

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

const defaultLimit = 1000
const defaultMetricStepSeconds = 60

type Repository struct {
	db driver.Conn
}

var _ query.SpanQueryRepository = (*Repository)(nil)
var _ query.TraceMetricRepository = (*Repository)(nil)

// NewRepository shares the Runtime-owned ClickHouse connection across reads.
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

type spanListScope string

const (
	spanScopeSession spanListScope = "session"
	spanScopeOwner   spanListScope = "owner"
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

type metricScope string

const (
	metricScopeSession metricScope = "session"
	metricScopeProject metricScope = "project"
	metricScopePerson  metricScope = "person"
)

func (r *Repository) GetBySession(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	return r.queryMetricBuckets(ctx, metricScopeSession, opts, "")
}

func (r *Repository) GetByProject(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	opts.AgentWorkID = ""
	return r.queryMetricBuckets(ctx, metricScopeProject, opts, "")
}

// GetByPerson reads a person's Works whatever their Project.
func (r *Repository) GetByPerson(ctx context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.ProjectID = ""
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	return r.queryMetricBuckets(ctx, metricScopePerson, opts, strings.TrimSpace(personID))
}

func (r *Repository) queryMetricBuckets(ctx context.Context, scope metricScope, opts query.TraceMetricOptions, personID string) (*query.TraceMetricResult, error) {
	q, params := buildMetricBucketsQuery(scope, opts, personID)
	rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), q)
	if err != nil {
		return nil, oops.With("operation", "trace.query").Wrap(err)
	}
	defer rows.Close()

	buckets, err := scanMetricBuckets(rows)
	if err != nil {
		return nil, oops.With("operation", "trace.scan").Wrap(err)
	}
	return &query.TraceMetricResult{
		Buckets: buckets,
		Totals:  metricTotals(buckets),
	}, nil
}

// personID is the person a person-scoped query reads; other scopes ignore it.
func buildMetricBucketsQuery(scope metricScope, opts query.TraceMetricOptions, personID string) (string, clickhousego.Parameters) {
	stepSeconds := opts.StepSeconds
	if stepSeconds <= 0 {
		stepSeconds = defaultMetricStepSeconds
	}
	params := clickhousego.Parameters{"step_seconds": strconv.Itoa(stepSeconds)}
	filters := []string{"measurement_time >= now64(9) - INTERVAL 30 DAY"}
	switch scope {
	case metricScopeProject:
		params["project_id"] = strings.TrimSpace(opts.ProjectID)
		filters = append(filters, "project_id = {project_id:String}")
	case metricScopePerson:
		// An empty person matches only Works of no person; never read them.
		params["user_id"] = personID
		filters = append(filters, "user_id = {user_id:String}", "user_id != ''")
		if workID := strings.TrimSpace(opts.AgentWorkID); workID != "" {
			params["agent_work_id"] = workID
			filters = append(filters, "agent_work_id = {agent_work_id:String}")
		}
	default:
		params["agent_work_id"] = strings.TrimSpace(opts.AgentWorkID)
		params["project_id"] = strings.TrimSpace(opts.ProjectID)
		filters = append(filters, "project_id = {project_id:String}", "agent_work_id = {agent_work_id:String}")
	}
	if !opts.StartTime.IsZero() {
		params["start_time"] = opts.StartTime.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "measurement_time >= {start_time:DateTime64(9, 'UTC')}")
	}
	if !opts.EndTime.IsZero() {
		params["end_time"] = opts.EndTime.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "measurement_time < {end_time:DateTime64(9, 'UTC')}")
	}
	metricKinds := normalizeFilterValues(opts.MetricKinds)
	if len(metricKinds) > 0 {
		params["metric_kinds"] = stringArrayParam(metricKinds)
		// Metric selection must not hide known interpretation gaps, including
		// calls whose category cannot yet be assigned to a requested metric.
		filters = append(filters, "(has({metric_kinds:Array(String)}, metric_kind) OR notEmpty(gaps))")
	}
	groupedBucketExpr := "toStartOfInterval(measurement_time, toIntervalSecond({step_seconds:UInt32}))"
	if !opts.StartTime.IsZero() {
		params["step_nanos"] = strconv.FormatInt(int64(stepSeconds)*int64(time.Second), 10)
		groupedBucketExpr = `fromUnixTimestamp64Nano(
				intDiv(
					toUnixTimestamp64Nano(measurement_time) - toUnixTimestamp64Nano({start_time:DateTime64(9, 'UTC')}),
					{step_nanos:Int64}
				) * {step_nanos:Int64} + toUnixTimestamp64Nano({start_time:DateTime64(9, 'UTC')}),
				'UTC'
			)`
	}

	q := fmt.Sprintf(`
		SELECT
			%s AS grouped_bucket_start,
			toUInt32({step_seconds:UInt32}) AS grouped_bucket_seconds,
			project_id,
			agent_work_id,
			agent_id,
			runtime_purpose,
			owner_context,
			owner_id,
			user_id,
			metric_kind,
			dimension,
			sum(value) AS metric_value,
			sum(unknown_count) AS metric_unknown_count,
			arrayDistinct(flatten(groupArray(gaps))) AS metric_gaps
		FROM trace_metric_buckets
		WHERE %s
		GROUP BY grouped_bucket_start, project_id, agent_work_id, agent_id, runtime_purpose, owner_context, owner_id, user_id, metric_kind, dimension
		ORDER BY grouped_bucket_start ASC, metric_kind ASC, dimension ASC, owner_context ASC, owner_id ASC, runtime_purpose ASC, agent_work_id ASC, user_id ASC`,
		groupedBucketExpr,
		strings.Join(filters, "\n\t\tAND "))
	return q, params
}

func scanMetricBuckets(rows driver.Rows) ([]query.TraceMetricBucket, error) {
	var out []query.TraceMetricBucket
	for rows.Next() {
		var bucket query.TraceMetricBucket
		var bucketSeconds uint32
		if err := rows.Scan(
			&bucket.BucketStart,
			&bucketSeconds,
			&bucket.ProjectID,
			&bucket.AgentWorkID,
			&bucket.AgentID,
			&bucket.RuntimePurpose,
			&bucket.OwnerContext,
			&bucket.OwnerID,
			&bucket.UserID,
			&bucket.MetricKind,
			&bucket.Dimension,
			&bucket.Value,
			&bucket.UnknownCount,
			&bucket.Gaps,
		); err != nil {
			return nil, err
		}
		bucket.BucketSeconds = int(bucketSeconds)
		out = append(out, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func metricTotals(buckets []query.TraceMetricBucket) []query.TraceMetricTotal {
	type key struct {
		metricKind string
		dimension  string
	}
	order := make([]key, 0)
	seen := map[key]int{}
	for _, bucket := range buckets {
		k := key{metricKind: bucket.MetricKind, dimension: bucket.Dimension}
		idx, ok := seen[k]
		if !ok {
			seen[k] = len(order)
			order = append(order, k)
			continue
		}
		_ = idx
	}
	totals := make([]query.TraceMetricTotal, len(order))
	for i, k := range order {
		totals[i] = query.TraceMetricTotal{MetricKind: k.metricKind, Dimension: k.dimension}
	}
	index := make(map[key]int, len(order))
	for i, k := range order {
		index[k] = i
	}
	for _, bucket := range buckets {
		k := key{metricKind: bucket.MetricKind, dimension: bucket.Dimension}
		totals[index[k]].Value += bucket.Value
		totals[index[k]].UnknownCount += bucket.UnknownCount
	}
	return totals
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

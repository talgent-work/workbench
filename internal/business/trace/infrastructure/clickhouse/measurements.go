package clickhouse

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// Derived measurements are read from trace_metric_buckets, never by
// rescanning native evidence.

const defaultMetricStepSeconds = 60

type metricScope string

const (
	metricScopeSession     metricScope = "session"
	metricScopeProject     metricScope = "project"
	metricScopePerson      metricScope = "person"
	metricScopeApplication metricScope = "application"
)

func (r *Repository) GetBySession(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	return r.queryMetricBuckets(ctx, metricScopeSession, opts, metricSubject{})
}

func (r *Repository) GetByProject(ctx context.Context, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	opts.AgentWorkID = ""
	return r.queryMetricBuckets(ctx, metricScopeProject, opts, metricSubject{})
}

// GetByPerson reads a person's Works whatever their Project.
func (r *Repository) GetByPerson(ctx context.Context, personID string, opts query.TraceMetricOptions) (*query.TraceMetricResult, error) {
	opts.ProjectID = ""
	opts.AgentWorkID = strings.TrimSpace(opts.AgentWorkID)
	return r.queryMetricBuckets(ctx, metricScopePerson, opts, metricSubject{personID: strings.TrimSpace(personID)})
}

// GetByApplication reads the Works of an Application, which never have a
// Project, narrowed to opts.WorkIDs when not nil.
func (r *Repository) GetByApplication(ctx context.Context, applicationID string, opts query.ApplicationMetricOptions) (*query.TraceMetricResult, error) {
	if opts.WorkIDs != nil && len(opts.WorkIDs) == 0 {
		return &query.TraceMetricResult{}, nil
	}
	opts.ProjectID, opts.AgentWorkID = "", ""
	return r.queryMetricBuckets(ctx, metricScopeApplication, opts.TraceMetricOptions, metricSubject{applicationID: strings.TrimSpace(applicationID), workIDs: opts.WorkIDs})
}

// ListApplicationWorks reads the Works an Application has measurements of
// within the window and measurement retention, at most limit.
func (r *Repository) ListApplicationWorks(ctx context.Context, applicationID string, start, end time.Time, limit int) ([]string, error) {
	params := clickhousego.Parameters{"application_id": strings.TrimSpace(applicationID)}
	filters := []string{"timestamp >= now64(9) - INTERVAL 30 DAY", "application_id = {application_id:String}", "application_id != ''", "project_id = ''"}
	if !start.IsZero() {
		params["start_time"] = start.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "timestamp >= {start_time:DateTime64(9, 'UTC')}")
	}
	if !end.IsZero() {
		params["end_time"] = end.UTC().Format("2006-01-02 15:04:05.999999999")
		filters = append(filters, "timestamp < {end_time:DateTime64(9, 'UTC')}")
	}
	q := fmt.Sprintf(`
		SELECT DISTINCT agent_work_id
		FROM trace_work_measurements
		WHERE %s
		ORDER BY agent_work_id
		LIMIT %d`, strings.Join(filters, "\n\t\tAND "), max(limit, 1))
	rows, err := r.db.Query(clickhousego.Context(ctx, clickhousego.WithParameters(params)), q)
	if err != nil {
		return nil, oops.With("operation", "trace.query").Wrap(err)
	}
	defer rows.Close()
	var works []string
	for rows.Next() {
		var work string
		if err := rows.Scan(&work); err != nil {
			return nil, oops.With("operation", "trace.scan").Wrap(err)
		}
		works = append(works, work)
	}
	if err := rows.Err(); err != nil {
		return nil, oops.With("operation", "trace.scan").Wrap(err)
	}
	return works, nil
}

// metricSubject is the person or the Application a scoped measurement read
// names; other scopes ignore it.
type metricSubject struct {
	personID, applicationID string
	workIDs                 []string
}

func (r *Repository) queryMetricBuckets(ctx context.Context, scope metricScope, opts query.TraceMetricOptions, subject metricSubject) (*query.TraceMetricResult, error) {
	q, params := buildMetricBucketsQuery(scope, opts, subject)
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
		Totals:  query.Totals(buckets),
	}, nil
}

func buildMetricBucketsQuery(scope metricScope, opts query.TraceMetricOptions, subject metricSubject) (string, clickhousego.Parameters) {
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
		params["user_id"] = subject.personID
		filters = append(filters, "user_id = {user_id:String}", "user_id != ''")
		if workID := strings.TrimSpace(opts.AgentWorkID); workID != "" {
			params["agent_work_id"] = workID
			filters = append(filters, "agent_work_id = {agent_work_id:String}")
		}
	case metricScopeApplication:
		// An empty Application matches only Works no Application owns.
		params["application_id"] = subject.applicationID
		filters = append(filters, "application_id = {application_id:String}", "application_id != ''", "project_id = ''")
		if subject.workIDs != nil {
			params["work_ids"] = stringArrayParam(subject.workIDs)
			filters = append(filters, "has({work_ids:Array(String)}, agent_work_id)")
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
			application_id,
			metric_kind,
			dimension,
			sum(value) AS metric_value,
			sum(unknown_count) AS metric_unknown_count,
			arrayDistinct(flatten(groupArray(gaps))) AS metric_gaps
		FROM trace_metric_buckets
		WHERE %s
		GROUP BY grouped_bucket_start, project_id, agent_work_id, agent_id, runtime_purpose, owner_context, owner_id, user_id, application_id, metric_kind, dimension
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
			&bucket.ApplicationID,
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

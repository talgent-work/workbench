//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

const (
	applicationA = "0195c9b8-0000-7000-8000-00000000000a"
	applicationB = "0195c9b8-0000-7000-8000-00000000000b"
	ownerA       = "0195c9b8-0000-7000-8000-0000000000a1"
)

// applicationAgent stands for Agent: it attributes only the Works each
// Application owns, with their Work Labels, and admits their owner.
type applicationAgent struct {
	works map[string]map[string]map[string]string
}

func (a applicationAgent) AuthorizeWorkReader(_ context.Context, readerID, workID string) error {
	if _, ok := a.works[readerID][workID]; ok {
		return nil
	}
	return query.ErrAuthorizationPermissionDenied
}

func (a applicationAgent) ListWorkAttributions(_ context.Context, applicationID string, workIDs []string) ([]query.WorkAttribution, error) {
	var works []query.WorkAttribution
	for _, id := range workIDs {
		if labels, ok := a.works[applicationID][id]; ok {
			works = append(works, query.WorkAttribution{WorkID: id, Labels: labels})
		}
	}
	return works, nil
}

type applicationOwners map[string]string

func (o applicationOwners) GetApplication(_ context.Context, applicationID string) (query.OwnedApplication, error) {
	owner, ok := o[applicationID]
	if !ok {
		return query.OwnedApplication{}, query.ErrApplicationUnknown
	}
	return query.OwnedApplication{ID: applicationID, OwnerUserID: owner, Removed: true}, nil
}

// seedApplicationUsage measures three Works of Application A, one of
// Application B and one of a person, with output tokens only.
func seedApplicationUsage(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	base := metricBucketBaseTime()
	rows := []struct {
		work, application, user string
		tokens                  int
	}{
		{"app-a-work-1", applicationA, "", 100},
		{"app-a-work-2", applicationA, "", 30},
		{"app-a-work-3", applicationA, "", 7},
		{"app-b-work-1", applicationB, "", 1000},
		{"personal-1", "", "user-1", 5000},
	}
	for index, row := range rows {
		id := fmt.Sprintf("application-fixture-%d", index)
		_, err := db.ExecContext(context.Background(), `INSERT INTO trace_work_measurements
            (evidence_id, measurement_id, timestamp, project_id, agent_work_id, runtime_purpose,
             user_id, application_id, dimension, measurements, source_priority, projection_version)
            SELECT ?, ?, ?, '', ?, 'work', ?, ?, 'claude-opus', [('output_tokens', CAST(? AS Nullable(Float64)))], 2, 1`,
			id, id, base.Add(time.Duration(index)*time.Minute), row.work, row.user, row.application, row.tokens)
		if err != nil {
			t.Fatalf("insert measurement: %v", err)
		}
	}
	return base
}

func applicationQueries(t *testing.T) (*query.Queries, *sql.DB) {
	t.Helper()
	dsn, cleanup := startClickHouse(t)
	t.Cleanup(cleanup)
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	agent := applicationAgent{works: map[string]map[string]map[string]string{
		applicationA: {
			"app-a-work-1": {"tenant": "acme", "user": "u1"},
			"app-a-work-2": {"tenant": "acme", "user": "u2"},
			"app-a-work-3": {"user": "u3"},
		},
		applicationB: {"app-b-work-1": {"tenant": "acme"}},
	}}
	return query.NewQueries(repo, repo, nil, agent, applicationOwners{applicationA: ownerA, applicationB: "someone-else"}), db
}

func asApplication(id string) context.Context {
	return authn.WithPrincipal(context.Background(), authn.ApplicationPrincipal(authn.Application{ID: id}))
}

func outputTokens(totals []query.TraceMetricTotal) float64 {
	var sum float64
	for _, total := range totals {
		if total.MetricKind == "output_tokens" {
			sum += total.Value
		}
	}
	return sum
}

// An Application reads only its own usage; another Application's Works, a
// person's Works and a request naming another Application stay out.
func TestApplicationUsageIsolatesApplications(t *testing.T) {
	queries, db := applicationQueries(t)
	base := seedApplicationUsage(t, db)
	window := query.TraceMetricOptions{StartTime: base, EndTime: base.Add(time.Hour), StepSeconds: 3600}

	own, err := queries.GetApplicationTraceMetrics(asApplication(applicationA), query.ApplicationUsageOptions{TraceMetricOptions: window})
	if err != nil {
		t.Fatal(err)
	}
	if got := outputTokens(own.Totals); got != 137 {
		t.Fatalf("Application A output tokens = %v, want 137 (%+v)", got, own.Totals)
	}
	for _, bucket := range own.Buckets {
		if bucket.ApplicationID != applicationA {
			t.Fatalf("bucket of another owner: %+v", bucket)
		}
	}
	if _, err := queries.GetApplicationTraceMetrics(asApplication(applicationA), query.ApplicationUsageOptions{ApplicationID: applicationB, TraceMetricOptions: window}); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("naming another Application: %v", err)
	}
	other, err := queries.GetApplicationTraceMetrics(asApplication(applicationB), query.ApplicationUsageOptions{TraceMetricOptions: window})
	if err != nil || outputTokens(other.Totals) != 1000 {
		t.Fatalf("Application B = %+v, %v", other, err)
	}
}

// The Application Owner reads the Application's usage, also after its
// removal; any other person is refused.
func TestApplicationUsageAdmitsItsOwnerOnly(t *testing.T) {
	queries, db := applicationQueries(t)
	base := seedApplicationUsage(t, db)
	window := query.TraceMetricOptions{StartTime: base, EndTime: base.Add(time.Hour), StepSeconds: 3600}
	owner := authn.WithAuthenticatedUser(context.Background(), authn.AuthenticatedUser{ID: ownerA})
	usage, err := queries.GetApplicationTraceMetrics(owner, query.ApplicationUsageOptions{ApplicationID: applicationA, TraceMetricOptions: window})
	if err != nil || outputTokens(usage.Totals) != 137 {
		t.Fatalf("owner read = %+v, %v", usage, err)
	}
	if _, err := queries.GetApplicationTraceMetrics(owner, query.ApplicationUsageOptions{ApplicationID: applicationB, TraceMetricOptions: window}); !errors.Is(err, query.ErrAuthorizationPermissionDenied) {
		t.Fatalf("another owner's Application: %v", err)
	}
	if _, err := queries.GetApplicationTraceMetrics(owner, query.ApplicationUsageOptions{TraceMetricOptions: window}); !errors.Is(err, query.ErrInvalidQuery) {
		t.Fatalf("a person must name the Application: %v", err)
	}
}

// A label filter resolves through Agent to the Works carrying every label,
// and a breakdown groups per-Work usage by the key's values, the Works
// without the key apart.
func TestApplicationUsageNarrowsAndBreaksDownByWorkLabels(t *testing.T) {
	queries, db := applicationQueries(t)
	base := seedApplicationUsage(t, db)
	window := query.TraceMetricOptions{StartTime: base, EndTime: base.Add(time.Hour), StepSeconds: 3600}

	acme, err := queries.GetApplicationTraceMetrics(asApplication(applicationA), query.ApplicationUsageOptions{TraceMetricOptions: window, Labels: map[string]string{"tenant": "acme"}})
	if err != nil || outputTokens(acme.Totals) != 130 {
		t.Fatalf("tenant=acme = %+v, %v; want 130, never Application B's acme Work", acme, err)
	}
	none, err := queries.GetApplicationTraceMetrics(asApplication(applicationA), query.ApplicationUsageOptions{TraceMetricOptions: window, Labels: map[string]string{"tenant": "globex"}})
	if err != nil || len(none.Buckets) != 0 || len(none.Totals) != 0 {
		t.Fatalf("no Work carries tenant=globex: %+v, %v", none, err)
	}

	byTenant, err := queries.GetApplicationTraceMetrics(asApplication(applicationA), query.ApplicationUsageOptions{TraceMetricOptions: window, BreakdownKey: "tenant", ListLabelKeys: true})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(byTenant.LabelKeys) != "[tenant user]" {
		t.Fatalf("label keys = %v, want the keys of Application A's measured Works", byTenant.LabelKeys)
	}
	if len(byTenant.Breakdown) != 2 {
		t.Fatalf("breakdown = %+v, want acme and the unlabelled Works", byTenant.Breakdown)
	}
	first, last := byTenant.Breakdown[0], byTenant.Breakdown[1]
	if first.Value != "acme" || !first.Labelled || first.WorkCount != 2 || outputTokens(first.Totals) != 130 {
		t.Fatalf("acme group = %+v", first)
	}
	if last.Labelled || last.WorkCount != 1 || outputTokens(last.Totals) != 7 {
		t.Fatalf("unlabelled group = %+v", last)
	}
	if outputTokens(byTenant.Totals) != 137 {
		t.Fatalf("breakdown changed the total: %+v", byTenant.Totals)
	}
}

// The Works an Application has measurements of are listed by its attribution
// alone, within the window.
func TestListApplicationWorksReadsItsMeasuredWorks(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	base := seedApplicationUsage(t, db)
	works, err := repo.ListApplicationWorks(context.Background(), applicationA, base, base.Add(time.Hour), 10)
	if err != nil || fmt.Sprint(works) != "[app-a-work-1 app-a-work-2 app-a-work-3]" {
		t.Fatalf("works = %v, %v", works, err)
	}
	later, err := repo.ListApplicationWorks(context.Background(), applicationA, base.Add(90*time.Second), time.Time{}, 10)
	if err != nil || fmt.Sprint(later) != "[app-a-work-3]" {
		t.Fatalf("works after the window start = %v, %v", later, err)
	}
	if empty, err := repo.ListApplicationWorks(context.Background(), "", base, time.Time{}, 10); err != nil || len(empty) != 0 {
		t.Fatalf("an empty Application reads nothing: %v, %v", empty, err)
	}
}

// An Application's spans are those of its Works, narrowed by labels, and
// never another Application's.
func TestListSpansByApplicationReadsItsWorks(t *testing.T) {
	queries, db := applicationQueries(t)
	seedApplicationUsage(t, db)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	spans := []struct{ work, application, span string }{
		{"app-a-work-1", applicationA, "a100000000000001"},
		{"app-a-work-3", applicationA, "a300000000000001"},
		{"app-b-work-1", applicationB, "b100000000000001"},
	}
	for index, row := range spans {
		attrs := clickhouseStringMapLiteral(map[string]string{"talgent.agent_work_id": row.work, "talgent.application_id": row.application})
		_, err := db.ExecContext(context.Background(), `INSERT INTO otel_traces
			(Timestamp, TraceId, SpanId, ParentSpanId, TraceState, SpanName, SpanKind, ServiceName,
			 ResourceAttributes, SpanAttributes, Duration, StatusCode, StatusMessage)
			VALUES (?, 'cccccccccccccccccccccccccccccccc', ?, '', '', 'claude_code.interaction', 'Internal', 'claude-code', `+attrs+`, map(), 1000, 'Ok', '')`,
			base.Add(time.Duration(index)*time.Second), row.span)
		if err != nil {
			t.Fatalf("insert span: %v", err)
		}
	}
	all, err := queries.ListSpansByApplication(asApplication(applicationA), query.ApplicationSpanOptions{})
	if err != nil || len(all.Spans) != 2 || all.Spans[0].AgentWorkID != "app-a-work-3" {
		t.Fatalf("Application A spans = %+v, %v; want its two, newest first", all, err)
	}
	acme, err := queries.ListSpansByApplication(asApplication(applicationA), query.ApplicationSpanOptions{Labels: map[string]string{"user": "u1"}})
	if err != nil || len(acme.Spans) != 1 || acme.Spans[0].AgentWorkID != "app-a-work-1" {
		t.Fatalf("user=u1 spans = %+v, %v", acme, err)
	}
}

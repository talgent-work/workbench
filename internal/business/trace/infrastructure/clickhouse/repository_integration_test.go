//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	clickhousego "github.com/ClickHouse/clickhouse-go/v2"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

func startClickHouse(t *testing.T) (string, func()) {
	t.Helper()
	return middlewaretest.ClickHouse(t, "talgent_trace"), func() {}
}

// seedOtelTracesSchema inserts the span fixtures in one statement. Uses a
// literal map(...) call for the attribute maps so the test doesn't depend on
// the driver's Map parameter encoding — what we care about here is the repo's
// SELECT behavior, not the INSERT path.
func seedOtelTracesSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	ctx := context.Background()

	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)

	rows := []struct {
		ts           time.Time
		traceID      string // hex
		spanID       string // hex
		name         string
		agentSession string
		duration     int64
	}{
		// session-42: inserted out of time order to prove ORDER BY works.
		// Keep timestamps seconds apart so ClickHouse driver/storage precision
		// differences cannot collapse the sort keys on slower CI runners.
		{base.Add(10 * time.Second), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2222222222222222", "claude_code.llm_request", "session-42", 2_000_000},
		{base, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "1111111111111111", "claude_code.interaction", "session-42", 5_000_000},
		{base.Add(15 * time.Second), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "5555555555555555", "claude_code.tool", "session-42", 1_000_000},
		// session-99: one row — should NOT appear in session-42 results.
		{base.Add(20 * time.Second), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "3333333333333333", "claude_code.interaction", "session-99", 1_000_000},
		// maintainer-session: non-Work row with its own AgentWork identity.
		{base.Add(50 * time.Second), "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", "3030303030303030", "claude_code.tool", "maintainer-session", 1_000_000},
	}

	values := make([]string, 0, len(rows))
	args := make([]any, 0, len(rows)*5)
	for _, r := range rows {
		attrs := map[string]string{"talgent.work_caller": "team:team-1", "talgent.agent_work_id": r.agentSession}
		if r.agentSession == "maintainer-session" {
			attrs["talgent.agent_id"] = "runtime-maintainer-1"
			attrs["talgent.runtime_purpose"] = "maintainer"
			attrs["talgent.owner_context"] = "caller"
			attrs["talgent.owner_id"] = "board-1"
		}
		values = append(values, fmt.Sprintf("(?, ?, ?, '0000000000000000', '', ?, 'Internal', 'claude-code', %s, %s, ?, 'Ok', '')",
			clickhouseStringMapLiteral(attrs), clickhouseStringMapLiteral(map[string]string{"tool_name": "Read", "tool_use_id": r.spanID})))
		args = append(args, r.ts, r.traceID, r.spanID, r.name, r.duration)
	}
	q := `INSERT INTO otel_traces
			(Timestamp, TraceId, SpanId, ParentSpanId, TraceState,
			 SpanName, SpanKind, ServiceName,
			 ResourceAttributes, SpanAttributes,
			 Duration, StatusCode, StatusMessage)
		VALUES ` + strings.Join(values, ", ")
	if _, err := db.ExecContext(ctx, q, args...); err != nil {
		t.Fatalf("insert span fixtures: %v", err)
	}
}

func clickhouseStringMapLiteral(attrs map[string]string) string {
	if len(attrs) == 0 {
		return "map()"
	}
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		value, ok := attrs[key]
		if !ok {
			continue
		}
		args = append(args, key, value)
	}
	q := "map("
	for i := 0; i < len(args); i += 2 {
		if i > 0 {
			q += ", "
		}
		q += fmt.Sprintf("'%s', '%s'", args[i], args[i+1])
	}
	q += ")"
	return q
}

// Intent: guard that the WHERE + ORDER BY in ListBySession correctly filter
// by ResourceAttributes['talgent.agent_work_id'] and return spans in ascending
// start_time order. If someone rewrites the SQL and forgets the map index,
// or flips the ORDER BY direction, this catches it.
func TestListBySession_ReturnsInsertedSpans(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatalf("NewSpanRepository: %v", err)
	}

	// Seed via a parallel sql.DB handle; repo's *sql.DB is unexported and
	// the test fixture isn't a production concern of the repo type.
	seedDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer seedDB.Close()
	seedOtelTracesSchema(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	page, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-1"}, AgentWorkID: "session-42"})
	spans := page.Spans
	if err != nil {
		t.Fatalf("ListBySession: %v", err)
	}
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans for session-42, got %d", len(spans))
	}
	if spans[0].Name != "claude_code.interaction" {
		t.Errorf("ORDER BY Timestamp ASC broken: first span name=%q, want %q", spans[0].Name, "claude_code.interaction")
	}
	if spans[1].Name != "claude_code.llm_request" {
		t.Errorf("ORDER BY Timestamp ASC broken: second span name=%q, want %q", spans[1].Name, "claude_code.llm_request")
	}
	if spans[2].Name != "claude_code.tool" {
		t.Errorf("ORDER BY Timestamp ASC broken: third span name=%q, want %q", spans[2].Name, "claude_code.tool")
	}
	if spans[0].AgentWorkID != "session-42" {
		t.Errorf("SessionID projection broken: got %q, want %q", spans[0].AgentWorkID, "session-42")
	}
	if spans[0].DurationNanos != 5_000_000 {
		t.Errorf("DurationNanos mismatch: got %d, want %d", spans[0].DurationNanos, 5_000_000)
	}
	if spans[0].ResourceAttributes["talgent.agent_work_id"] != "session-42" {
		t.Errorf("ResourceAttributes not projected: got %v", spans[0].ResourceAttributes)
	}
}

// Intent: Session trace queries page by (Timestamp, SpanId) within their scope.
func TestListBySession_PaginatesWithinSession(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatalf("NewSpanRepository: %v", err)
	}
	seedDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer seedDB.Close()
	seedOtelTracesSchema(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	firstPage, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-1"}, AgentWorkID: "session-42", Limit: 1})
	if err != nil {
		t.Fatalf("ListBySession first page: %v", err)
	}
	if len(firstPage.Spans) != 1 {
		t.Fatalf("first page spans len = %d, want 1", len(firstPage.Spans))
	}
	if firstPage.Spans[0].SpanID != "1111111111111111" {
		t.Fatalf("first page span = %q, want first timestamp/span id", firstPage.Spans[0].SpanID)
	}
	if firstPage.Spans[0].AgentWorkID != "session-42" {
		t.Fatalf("AgentWorkID = %q, want session-42", firstPage.Spans[0].AgentWorkID)
	}
	if firstPage.NextCursor == "" {
		t.Fatal("first page next cursor is empty, want cursor for remaining rows")
	}

	secondPage, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-1"},
		AgentWorkID: "session-42",
		Limit:       10,
		AfterCursor: firstPage.NextCursor,
	})
	if err != nil {
		t.Fatalf("ListBySession second page: %v", err)
	}
	if len(secondPage.Spans) != 2 {
		t.Fatalf("second page spans len = %d, want 2", len(secondPage.Spans))
	}
	if secondPage.Spans[0].SpanID != "2222222222222222" || secondPage.Spans[1].SpanID != "5555555555555555" {
		t.Fatalf("second page order = [%s %s], want [2222 5555]", secondPage.Spans[0].SpanID, secondPage.Spans[1].SpanID)
	}
	if secondPage.NextCursor != "" {
		t.Fatalf("second page next cursor = %q, want empty", secondPage.NextCursor)
	}

}

// Intent: a newest-first reader sees the latest activity on the first page and
// its cursor continues backwards through the same spans, without gaps or repeats.
func TestListBySession_PaginatesNewestFirst(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatalf("NewSpanRepository: %v", err)
	}
	seedDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer seedDB.Close()
	seedOtelTracesSchema(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var got []string
	cursor := ""
	for page := 0; page < 4; page++ {
		result, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-1"}, AgentWorkID: "session-42",
			Limit: 2, AfterCursor: cursor, NewestFirst: true})
		if err != nil {
			t.Fatalf("ListBySession page %d: %v", page, err)
		}
		for _, span := range result.Spans {
			got = append(got, span.SpanID)
		}
		if cursor = result.NextCursor; cursor == "" {
			break
		}
	}
	want := []string{"5555555555555555", "2222222222222222", "1111111111111111"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("newest-first spans = %v, want %v", got, want)
	}
}

// Intent: owner scoped span queries must filter by Work Caller + owner identity and
// optional runtime purpose without requiring agent_work_id.
func TestListByOwner_ReturnsNonWorkOwnerNeutralSpan(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatalf("NewSpanRepository: %v", err)
	}
	seedDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer seedDB.Close()
	seedOtelTracesSchema(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := repo.ListByWorkCaller(ctx, query.SpanListOptions{
		WorkCaller:     query.WorkCaller{Kind: "team", ID: "team-1"},
		OwnerContext:   "caller",
		OwnerID:        "board-1",
		RuntimePurpose: "maintainer",
	})
	if err != nil {
		t.Fatalf("ListByWorkCaller: %v", err)
	}
	if len(result.Spans) != 1 {
		t.Fatalf("spans len = %d, want 1", len(result.Spans))
	}
	if result.Spans[0].AgentWorkID != "maintainer-session" {
		t.Fatalf("AgentWorkID = %q, want maintainer-session", result.Spans[0].AgentWorkID)
	}
}

// Intent: metrics APIs must aggregate precomputed trace_metric_buckets rows
// into the requested window so Portal charts are served without raw span scans.
func TestGetBySessionMetrics_AggregatesMetricBuckets(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatalf("NewTraceMetricRepository: %v", err)
	}
	seedDB, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatalf("seed open: %v", err)
	}
	defer seedDB.Close()
	base := seedMetricBuckets(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := repo.GetBySession(ctx, query.TraceMetricOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-1"},
		AgentWorkID: "work-1",
		StartTime:   base,
		EndTime:     base.Add(10 * time.Minute),
		StepSeconds: 300,
		MetricKinds: []string{"tool_call", "skill_call"},
	})
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if len(result.Buckets) != 2 {
		t.Fatalf("buckets len = %d, want 2 (%+v)", len(result.Buckets), result.Buckets)
	}
	if result.Buckets[0].MetricKind != "skill_call" || result.Buckets[0].Dimension != "designing-tests" || result.Buckets[0].Value != 1 {
		t.Fatalf("first bucket = %+v, want skill_call/designing-tests/1", result.Buckets[0])
	}
	if result.Buckets[1].MetricKind != "tool_call" || result.Buckets[1].Dimension != "Read" || result.Buckets[1].Value != 3 {
		t.Fatalf("second bucket = %+v, want tool_call/Read/3", result.Buckets[1])
	}
	if result.Buckets[1].OwnerID != "case-alpha" || result.Buckets[1].UserID != "user-1" {
		t.Fatalf("second bucket attribution = owner:%q user:%q, want case-alpha/user-1", result.Buckets[1].OwnerID, result.Buckets[1].UserID)
	}
	if len(result.Totals) != 2 {
		t.Fatalf("totals len = %d, want 2 (%+v)", len(result.Totals), result.Totals)
	}
	if result.Totals[1].MetricKind != "tool_call" || result.Totals[1].Dimension != "Read" || result.Totals[1].Value != 3 {
		t.Fatalf("tool total = %+v, want tool_call/Read/3", result.Totals[1])
	}
}

// Intent: Work Caller metrics must remain scoped to the Work Caller and include
// rows from multiple works without enumerating those works through another
// service; a Work Key narrows them to that key's Works.
// Intent: Work Caller metrics must include non-Work owner-neutral rows rather
// than requiring a Work-owned Session.
func seedMetricBuckets(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	ctx := context.Background()
	base := metricBucketBaseTime()
	rows := []struct {
		ts             time.Time
		workCaller     string
		workKey        string
		sessionID      string
		agentID        string
		runtimePurpose string
		ownerContext   string
		ownerID        string
		userID         string
		metricKind     string
		dimension      string
		value          float64
	}{
		{ts: base, workCaller: "team:team-1", workKey: "board:board-1", sessionID: "work-1", runtimePurpose: "work", ownerContext: "case", ownerID: "case-alpha", userID: "user-1", metricKind: "tool_call", dimension: "Read", value: 1},
		{ts: base.Add(time.Minute), workCaller: "team:team-1", workKey: "board:board-1", sessionID: "work-1", runtimePurpose: "work", ownerContext: "case", ownerID: "case-alpha", userID: "user-1", metricKind: "tool_call", dimension: "Read", value: 2},
		{ts: base.Add(2 * time.Minute), workCaller: "team:team-1", workKey: "board:board-1", sessionID: "work-1", runtimePurpose: "work", ownerContext: "case", ownerID: "case-alpha", userID: "user-1", metricKind: "skill_call", dimension: "designing-tests", value: 1},
		{ts: base.Add(3 * time.Minute), workCaller: "team:team-1", workKey: "board:board-2", sessionID: "work-2", runtimePurpose: "normal", ownerContext: "case", ownerID: "case-beta", userID: "user-2", metricKind: "tool_call", dimension: "Read", value: 4},
		{ts: base.Add(4 * time.Minute), workCaller: "team:team-2", workKey: "board:board-1", sessionID: "work-3", runtimePurpose: "work", ownerContext: "case", ownerID: "case-other", userID: "user-3", metricKind: "tool_call", dimension: "Read", value: 8},
		// A Standalone Work of the same person as work-1.
		{ts: base.Add(5 * time.Minute), workCaller: "person:user-1", sessionID: "personal-1", runtimePurpose: "work", userID: "user-1", metricKind: "tool_call", dimension: "Read", value: 5},
	}
	for index, row := range rows {
		// Query-only fixtures: one already-interpreted fact per call. Native
		// evidence interpretation is exercised through the real Collector suite.
		for call := 0; call < int(row.value); call++ {
			id := fmt.Sprintf("fixture-%d-%d", index, call)
			_, err := db.ExecContext(ctx, `INSERT INTO trace_work_measurements
                (evidence_id, measurement_id, timestamp, work_caller, work_key, agent_work_id,
                 agent_id, runtime_purpose, owner_context, owner_id, user_id,
                 dimension, measurements, source_priority, projection_version)
                SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, [(?, CAST(1 AS Nullable(Float64)))], 2, 1`,
				id, id, row.ts, row.workCaller, row.workKey, row.sessionID, row.agentID,
				row.runtimePurpose, row.ownerContext, row.ownerID, row.userID, row.dimension, row.metricKind)
			if err != nil {
				t.Fatalf("insert measurement: %v", err)
			}
		}
	}

	return base
}

func metricBucketBaseTime() time.Time {
	// Keep rows within the table's 30-day TTL so the integration test is not date-sensitive.
	return time.Now().UTC().Truncate(time.Hour).Add(-24 * time.Hour)
}

// Tests own the connection lifecycle; production ownership is in Runtime.
func openTestRepository(t *testing.T, dsn string) (*Repository, error) {
	t.Helper()
	options, err := clickhousego.ParseDSN(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := clickhousego.Open(options)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewRepository(conn), nil
}

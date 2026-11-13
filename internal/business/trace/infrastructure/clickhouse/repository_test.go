package clickhouse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

var teamCaller = query.WorkCaller{Kind: "team", ID: "team-1"}

// Intent: owner-neutral span identity must be projected from OTEL attributes so
// API authorization and responses do not depend on Work-only agent_work_id;
// the Work Caller and Work Key split at their first colon, as Agent writes
// them.
func TestSpanScopeFromAttributesMapsOwnerNeutralFields(t *testing.T) {
	got := spanScopeFromAttributes(map[string]string{
		"talgent.work_caller":     "team:team-1",
		"talgent.work_key":        "board:board:7",
		"talgent.execution_id":    "legacy-run",
		"talgent.agent_id":        "runtime-1",
		"talgent.runtime_purpose": "maintainer",
		"talgent.owner_context":   "caller",
		"talgent.owner_id":        "board-1",
		"talgent.agent_work_id":   "work-1",
	})

	if got.WorkCaller != teamCaller ||
		got.WorkKey != (query.WorkKey{Kind: "board", ID: "board:7"}) ||
		got.AgentID != "runtime-1" ||
		got.RuntimePurpose != "maintainer" ||
		got.OwnerContext != "caller" ||
		got.OwnerID != "board-1" ||
		got.AgentWorkID != "work-1" {
		t.Fatalf("spanScopeFromAttributes() = %+v", got)
	}
	if none := spanScopeFromAttributes(map[string]string{"talgent.agent_work_id": "work-1"}); none.WorkCaller.Present() || none.WorkKey.Present() {
		t.Fatalf("Work facts without attributes = %+v", none)
	}
}

func TestBuildMetricBucketsQueryUsesMetricBucketsOnly(t *testing.T) {
	query, params := buildMetricBucketsQuery(metricScopeSession, query.TraceMetricOptions{WorkCaller: teamCaller, AgentWorkID: "work-1", StepSeconds: 300}, "")

	if !strings.Contains(query, "trace_metric_buckets") {
		t.Fatalf("query = %s, want trace_metric_buckets source", query)
	}
	if strings.Contains(query, "otel_traces") {
		t.Fatalf("query = %s, must not scan raw otel_traces", query)
	}
	for _, want := range []string{"agent_work_id = {agent_work_id:String}", "work_caller = {work_caller:String}", "work_caller != ''"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query = %s, want Work scope filter %q", query, want)
		}
	}
	if params["work_caller"] != "team:team-1" {
		t.Fatalf("params = %v", params)
	}
	if strings.Contains(query, "runtime_purpose IN") {
		t.Fatalf("Session metrics must include every runtime purpose: %s", query)
	}
	for _, column := range []string{"agent_id", "runtime_purpose", "owner_context", "owner_id", "user_id"} {
		if !strings.Contains(query, column) {
			t.Fatalf("query = %s, want owner-neutral column %q", query, column)
		}
	}
	if strings.Contains(query, "execution_id") || strings.Contains(query, "project_id") || strings.Contains(query, "application_id") {
		t.Fatalf("query = %s, metrics are aggregated per Work under its Work Caller", query)
	}
	if !strings.Contains(query, "GROUP BY grouped_bucket_start, work_caller, work_key, agent_work_id, agent_id, runtime_purpose, owner_context, owner_id, user_id, metric_kind, dimension") {
		t.Fatalf("query = %s, want Work facts and owner-neutral attribution columns in grouping", query)
	}
	if !strings.Contains(query, "ORDER BY grouped_bucket_start ASC, metric_kind ASC, dimension ASC, owner_context ASC, owner_id ASC, runtime_purpose ASC, agent_work_id ASC, user_id ASC") {
		t.Fatalf("query = %s, want deterministic owner-neutral attribution ordering", query)
	}
}

// Intent: Work Caller metrics must not require agent_work_id, so rows a caller
// records outside any Work contribute to its token and tool charts, and a
// Work Key narrows them to that key's Works.
func TestBuildMetricBucketsQuery_WorkCallerScopeDoesNotRequireAgentWorkID(t *testing.T) {
	sql, params := buildMetricBucketsQuery(metricScopeWorkCaller, query.TraceMetricOptions{WorkCaller: teamCaller}, "")

	if !strings.Contains(sql, "work_caller = {work_caller:String}") || params["work_caller"] != "team:team-1" {
		t.Fatalf("query = %s params = %v, want Work Caller scope filter", sql, params)
	}
	if strings.Contains(sql, "agent_work_id = {agent_work_id:String}") || strings.Contains(sql, "work_key =") || strings.Contains(sql, "work_ids") {
		t.Fatalf("query = %s, Work Caller metrics read every Work of the caller", sql)
	}
	keyed, params := buildMetricBucketsQuery(metricScopeWorkCaller, query.TraceMetricOptions{WorkCaller: teamCaller, WorkKey: query.WorkKey{Kind: "board", ID: "board-1"}}, "")
	if !strings.Contains(keyed, "work_key = {work_key:String}") || params["work_key"] != "board:board-1" {
		t.Fatalf("query = %s params = %v, want the Work Key filter", keyed, params)
	}
}

// A person's usage spans every Work Caller: it is filtered by the person,
// never by a Work Caller, and an empty person reads nothing rather than every
// Work of no person.
func TestBuildMetricBucketsQuery_PersonScopeFiltersThePersonAcrossWorkCallers(t *testing.T) {
	query, params := buildMetricBucketsQuery(metricScopePerson, query.TraceMetricOptions{WorkCaller: teamCaller, AgentWorkID: "work-1"}, "person-1")
	for _, want := range []string{"user_id = {user_id:String}", "user_id != ''", "agent_work_id = {agent_work_id:String}"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query = %s, want %q", query, want)
		}
	}
	if strings.Contains(query, "work_caller = {work_caller:String}") || params["user_id"] != "person-1" || params["agent_work_id"] != "work-1" {
		t.Fatalf("query = %s params = %v, want the person across Work Callers", query, params)
	}
}

// An Application's usage reads the Works whose Work Caller it is, narrowed to
// the Works a label filter resolved; the empty Work Caller reads nothing
// rather than every Work without one.
func TestBuildMetricBucketsQuery_ApplicationReadsItsWorkCallersWorks(t *testing.T) {
	sql, params := buildMetricBucketsQuery(metricScopeWorkCaller, query.TraceMetricOptions{WorkCaller: query.ApplicationCaller("application-1"), WorkIDs: []string{"work-1", "work'2"}}, "")
	for _, want := range []string{"work_caller = {work_caller:String}", "work_caller != ''", "has({work_ids:Array(String)}, agent_work_id)"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("query = %s, want %q", sql, want)
		}
	}
	if params["work_caller"] != "application:application-1" || params["work_ids"] != `['work-1', 'work\'2']` {
		t.Fatalf("params = %v", params)
	}
}

// A Work Caller's spans are listed by its attribution, newest first, narrowed
// to resolved Works when a filter applies.
func TestBuildSpanListQuery_WorkCallerScopeReadsItsWorks(t *testing.T) {
	sql, params, err := buildSpanListQuery(spanScopeWorkCaller, query.SpanListOptions{WorkCaller: query.ApplicationCaller("application-1"), WorkIDs: []string{"work-1"}, NewestFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ResourceAttributes['talgent.work_caller'] = {work_caller:String}",
		"ResourceAttributes['talgent.work_caller'] != ''",
		"has({work_ids:Array(String)}, ResourceAttributes['talgent.agent_work_id'])",
		"ORDER BY Timestamp DESC",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("query = %s, want %q", sql, want)
		}
	}
	if strings.Contains(sql, "owner_context") || params["work_caller"] != "application:application-1" {
		t.Fatalf("query = %s params = %v", sql, params)
	}
}

// Intent: owner span lists must use Work Caller + owner scope, allow optional
// runtime purpose, and keep Work-style cursor filters.
func TestBuildSpanListQuery_OwnerScopeFiltersWorkCallerOwnerAndPurpose(t *testing.T) {
	query, _, err := buildSpanListQuery(spanScopeWorkCaller, query.SpanListOptions{
		WorkCaller:     teamCaller,
		OwnerContext:   "caller",
		OwnerID:        "board-1",
		RuntimePurpose: "maintainer",
		AfterCursor:    mustEncodeSpanCursorForTest(t),
	})
	if err != nil {
		t.Fatalf("buildSpanListQuery: %v", err)
	}

	for _, want := range []string{
		"ResourceAttributes['talgent.work_caller'] = {work_caller:String}",
		"ResourceAttributes['talgent.owner_context'] = {owner_context:String}",
		"ResourceAttributes['talgent.owner_id'] = {owner_id:String}",
		"ResourceAttributes['talgent.runtime_purpose'] = {runtime_purpose:String}",
		"Timestamp > {after_time:DateTime64(9, 'UTC')}",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query = %s, want %q", query, want)
		}
	}
}

// Intent: chart buckets with an explicit start_time must align to that
// request boundary without relying on a ClickHouse overload absent in CI.
func TestBuildMetricBucketsQueryAnchorsBucketsToStartTime(t *testing.T) {
	start := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	query, params := buildMetricBucketsQuery(metricScopeWorkCaller, query.TraceMetricOptions{
		WorkCaller:  teamCaller,
		StartTime:   start,
		StepSeconds: 7200,
	}, "")

	for _, want := range []string{
		"fromUnixTimestamp64Nano(",
		"toUnixTimestamp64Nano(measurement_time) - toUnixTimestamp64Nano({start_time:DateTime64(9, 'UTC')})",
		"{step_nanos:Int64}",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("query = %s, want anchored bucket expression containing %q", query, want)
		}
	}
	if strings.Contains(query, "toStartOfInterval(measurement_time, toIntervalSecond({step_seconds:UInt32}), {start_time:DateTime64(9, 'UTC')})") {
		t.Fatalf("query = %s, must not use ClickHouse origin overload unsupported by CI", query)
	}
	if params["start_time"] != "2026-05-31 15:00:00" {
		t.Fatalf("start_time param = %q, want exact UTC origin", params["start_time"])
	}
	if params["step_nanos"] != "7200000000000" {
		t.Fatalf("step_nanos param = %q, want 2h in nanoseconds", params["step_nanos"])
	}
}

func TestClickHouseMigrationUsesDedicatedPrincipal(t *testing.T) {
	initRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "deploy", "middleware", "init", "clickhouse", "001-trace.sql"))
	if err != nil {
		t.Fatalf("read ClickHouse init SQL: %v", err)
	}
	if strings.Contains(string(initRaw), "DROP TABLE") {
		t.Fatal("OTEL collection principal must not receive DROP TABLE")
	}

	bootstrapRaw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "scripts", "deploy", "bootstrap-clickhouse.sh"))
	if err != nil {
		t.Fatalf("read ClickHouse bootstrap script: %v", err)
	}
	bootstrap := string(bootstrapRaw)
	for _, want := range []string{
		`CLICKHOUSE_MIGRATION_USER="${CLICKHOUSE_MIGRATION_USER:-default}"`,
		`--user="${CLICKHOUSE_MIGRATION_USER}"`,
		`--password="${CLICKHOUSE_MIGRATION_PASSWORD}"`,
	} {
		if !strings.Contains(bootstrap, want) {
			t.Fatalf("ClickHouse bootstrap script missing %q", want)
		}
	}
	if strings.Contains(bootstrap, "${CLICKHOUSE_USER:-otel}") {
		t.Fatal("ClickHouse bootstrap must not fall back to the OTEL collection principal")
	}
}

func mustEncodeSpanCursorForTest(t *testing.T) string {
	t.Helper()
	cursor, err := encodeEvidenceCursor(time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC), "1111111111111111")
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	return cursor
}

package clickhouse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// Intent: owner-neutral span identity must be projected from OTEL attributes so
// API authorization and responses do not depend on Work-only agent_work_id.
func TestSpanScopeFromAttributesMapsOwnerNeutralFields(t *testing.T) {
	got := spanScopeFromAttributes(map[string]string{
		"talgent.project_id":      "project-1",
		"talgent.execution_id":    "legacy-run",
		"talgent.agent_id":        "runtime-1",
		"talgent.runtime_purpose": "maintainer",
		"talgent.owner_context":   "caller",
		"talgent.owner_id":        "project-1",
		"talgent.agent_work_id":   "work-1",
	})

	if got.ProjectID != "project-1" ||
		got.AgentID != "runtime-1" ||
		got.RuntimePurpose != "maintainer" ||
		got.OwnerContext != "caller" ||
		got.OwnerID != "project-1" ||
		got.AgentWorkID != "work-1" {
		t.Fatalf("spanScopeFromAttributes() = %+v", got)
	}
}

func TestBuildMetricBucketsQueryUsesMetricBucketsOnly(t *testing.T) {
	query, _ := buildMetricBucketsQuery(metricScopeSession, query.TraceMetricOptions{AgentWorkID: "work-1", StepSeconds: 300}, "")

	if !strings.Contains(query, "trace_metric_buckets") {
		t.Fatalf("query = %s, want trace_metric_buckets source", query)
	}
	if strings.Contains(query, "otel_traces") {
		t.Fatalf("query = %s, must not scan raw otel_traces", query)
	}
	if !strings.Contains(query, "agent_work_id = {agent_work_id:String}") {
		t.Fatalf("query = %s, want agent_work_id scope filter", query)
	}
	if strings.Contains(query, "runtime_purpose IN") {
		t.Fatalf("Session metrics must include every runtime purpose: %s", query)
	}
	for _, column := range []string{"agent_id", "runtime_purpose", "owner_context", "owner_id", "user_id"} {
		if !strings.Contains(query, column) {
			t.Fatalf("query = %s, want owner-neutral column %q", query, column)
		}
	}
	if strings.Contains(query, "execution_id") {
		t.Fatalf("query = %s, metrics are aggregated per Work, never per process run", query)
	}
	if !strings.Contains(query, "GROUP BY grouped_bucket_start, project_id, agent_work_id, agent_id, runtime_purpose, owner_context, owner_id, user_id, metric_kind, dimension") {
		t.Fatalf("query = %s, want owner-neutral attribution columns in grouping", query)
	}
	if !strings.Contains(query, "ORDER BY grouped_bucket_start ASC, metric_kind ASC, dimension ASC, owner_context ASC, owner_id ASC, runtime_purpose ASC, agent_work_id ASC, user_id ASC") {
		t.Fatalf("query = %s, want deterministic owner-neutral attribution ordering", query)
	}
}

// Intent: project metrics must not require agent_work_id, so rows a caller
// records outside any Work contribute to project-level token and tool charts.
func TestBuildMetricBucketsQuery_ProjectScopeDoesNotRequireAgentWorkID(t *testing.T) {
	query, _ := buildMetricBucketsQuery(metricScopeProject, query.TraceMetricOptions{ProjectID: "project-1"}, "")

	if !strings.Contains(query, "project_id = {project_id:String}") {
		t.Fatalf("query = %s, want project scope filter", query)
	}
	if strings.Contains(query, "agent_work_id = {agent_work_id:String}") {
		t.Fatalf("query = %s, project metrics must not filter by agent_work_id", query)
	}
	if strings.Contains(query, "agent_work_id != ''") {
		t.Fatalf("query = %s, project metrics must include non-Work rows", query)
	}
}

// A person's usage spans every Project: it is filtered by the person, never
// by a Project, and an empty person reads nothing rather than every Work of
// no person.
func TestBuildMetricBucketsQuery_PersonScopeFiltersThePersonAcrossProjects(t *testing.T) {
	query, params := buildMetricBucketsQuery(metricScopePerson, query.TraceMetricOptions{ProjectID: "ignored", AgentWorkID: "work-1"}, "person-1")
	for _, want := range []string{"user_id = {user_id:String}", "user_id != ''", "agent_work_id = {agent_work_id:String}"} {
		if !strings.Contains(query, want) {
			t.Fatalf("query = %s, want %q", query, want)
		}
	}
	if strings.Contains(query, "project_id = {project_id:String}") || params["user_id"] != "person-1" || params["agent_work_id"] != "work-1" {
		t.Fatalf("query = %s params = %v, want the person across Projects", query, params)
	}
}

// Intent: owner span lists must use project + owner scope, allow optional
// runtime purpose, and keep Work-style cursor filters.
func TestBuildSpanListQuery_OwnerScopeFiltersProjectOwnerAndPurpose(t *testing.T) {
	query, _, err := buildSpanListQuery(spanScopeOwner, query.SpanListOptions{
		ProjectID:      "project-1",
		OwnerContext:   "caller",
		OwnerID:        "project-1",
		RuntimePurpose: "maintainer",
		AfterCursor:    mustEncodeSpanCursorForTest(t),
	})
	if err != nil {
		t.Fatalf("buildSpanListQuery: %v", err)
	}

	for _, want := range []string{
		"ResourceAttributes['talgent.project_id'] = {project_id:String}",
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
	query, params := buildMetricBucketsQuery(metricScopeProject, query.TraceMetricOptions{
		ProjectID:   "project-1",
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

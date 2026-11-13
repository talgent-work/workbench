//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

func TestGetByWorkCallerMetrics_AggregatesWorkCallerScope(t *testing.T) {
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
	seedMetricBuckets(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := repo.GetByWorkCaller(ctx, query.TraceMetricOptions{
		WorkCaller:  query.WorkCaller{Kind: "team", ID: "team-1"},
		StepSeconds: 300,
		MetricKinds: []string{"tool_call"},
	})
	if err != nil {
		t.Fatalf("GetByWorkCaller: %v", err)
	}
	if len(result.Totals) != 1 {
		t.Fatalf("totals len = %d, want 1 (%+v)", len(result.Totals), result.Totals)
	}
	if result.Totals[0].MetricKind != "tool_call" || result.Totals[0].Dimension != "Read" || result.Totals[0].Value != 7 {
		t.Fatalf("Work Caller tool total = %+v, want tool_call/Read/7", result.Totals[0])
	}
	if len(result.Buckets) != 2 {
		t.Fatalf("Work Caller buckets len = %d, want 2 attributed buckets (%+v)", len(result.Buckets), result.Buckets)
	}
	if result.Buckets[0].OwnerID != "case-alpha" || result.Buckets[0].UserID != "user-1" || result.Buckets[0].Value != 3 {
		t.Fatalf("first Work Caller bucket = %+v, want case-alpha/user-1 value 3", result.Buckets[0])
	}
	if result.Buckets[1].OwnerID != "case-beta" || result.Buckets[1].UserID != "user-2" || result.Buckets[1].Value != 4 {
		t.Fatalf("second Work Caller bucket = %+v, want case-beta/user-2 value 4", result.Buckets[1])
	}
	if result.Buckets[0].WorkCaller != (query.WorkCaller{Kind: "team", ID: "team-1"}) || result.Buckets[0].WorkKey != (query.WorkKey{Kind: "board", ID: "board-1"}) {
		t.Fatalf("first bucket Work facts = %+v %+v", result.Buckets[0].WorkCaller, result.Buckets[0].WorkKey)
	}
	keyed, err := repo.GetByWorkCaller(ctx, query.TraceMetricOptions{
		WorkCaller:  query.WorkCaller{Kind: "team", ID: "team-1"},
		WorkKey:     query.WorkKey{Kind: "board", ID: "board-2"},
		MetricKinds: []string{"tool_call"},
	})
	if err != nil {
		t.Fatalf("GetByWorkCaller by Work Key: %v", err)
	}
	if len(keyed.Totals) != 1 || keyed.Totals[0].Value != 4 || keyed.Buckets[0].AgentWorkID != "work-2" {
		t.Fatalf("Work Key usage = %+v, want work-2's 4 calls", keyed)
	}
}

func TestGetByWorkCallerMetrics_IncludesOwnerNeutralRows(t *testing.T) {
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
	base := seedOwnerNeutralMetricBucket(t, seedDB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, err := repo.GetByWorkCaller(ctx, query.TraceMetricOptions{
		WorkCaller:  query.WorkCaller{Kind: "team", ID: "team-1"},
		StartTime:   base,
		EndTime:     base.Add(5 * time.Minute),
		StepSeconds: 60,
	})
	if err != nil {
		t.Fatalf("GetByWorkCaller: %v", err)
	}
	if len(result.Buckets) != 1 {
		t.Fatalf("buckets len = %d, want 1 (%+v)", len(result.Buckets), result.Buckets)
	}
	got := result.Buckets[0]
	if got.AgentWorkID != "maintainer-session" || got.RuntimePurpose != "maintainer" || got.OwnerContext != "caller" || got.OwnerID != "board-1" || got.Value != 5 {
		t.Fatalf("owner-neutral Work Caller bucket = %+v", got)
	}
}

func seedOwnerNeutralMetricBucket(t *testing.T, db *sql.DB) time.Time {
	t.Helper()
	ctx := context.Background()
	base := metricBucketBaseTime()
	for call := 0; call < 5; call++ {
		id := fmt.Sprintf("owner-fixture-%d", call)
		_, err := db.ExecContext(ctx, `INSERT INTO trace_work_measurements
            (evidence_id, measurement_id, timestamp, work_caller, agent_work_id,
             agent_id, runtime_purpose, owner_context, owner_id, dimension, measurements, source_priority, projection_version)
            SELECT ?, ?, ?, 'team:team-1', 'maintainer-session', 'runtime-maintainer-1',
                'maintainer', 'caller', 'board-1', 'Read', [('tool_call', CAST(1 AS Nullable(Float64)))], 2, 1`, id, id, base)
		if err != nil {
			t.Fatalf("insert owner measurement: %v", err)
		}
	}

	return base
}

// A Work's Work Caller is the one its measurements carry: measurements
// without one say nothing, and two distinct Work Callers are an error rather
// than a pick.
func TestGetWorkCallerIgnoresUnattributedAndRefusesMixed(t *testing.T) {
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
	base := metricBucketBaseTime()
	for index, row := range []struct{ work, caller string }{
		{"stray-owned", ""}, {"stray-owned", "person:user-1"}, {"stray-owned", ""},
		{"mixed", "team:team-1"}, {"mixed", "team:team-2"},
		{"unattributed", ""},
	} {
		id := fmt.Sprintf("caller-fixture-%d", index)
		if _, err := db.ExecContext(context.Background(), `INSERT INTO trace_work_measurements
            (evidence_id, measurement_id, timestamp, work_caller, agent_work_id, dimension, measurements, source_priority, projection_version)
            SELECT ?, ?, ?, ?, ?, 'Read', [('tool_call', CAST(1 AS Nullable(Float64)))], 2, 1`,
			id, id, base.Add(time.Duration(index)*time.Minute), row.caller, row.work); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	if caller, observed, err := repo.GetWorkCaller(ctx, "stray-owned"); err != nil || !observed || caller != query.PersonCaller("user-1") {
		t.Fatalf("stray-owned = %+v %v %v", caller, observed, err)
	}
	if _, observed, err := repo.GetWorkCaller(ctx, "unattributed"); err != nil || observed {
		t.Fatalf("unattributed observed = %v, %v", observed, err)
	}
	if _, _, err := repo.GetWorkCaller(ctx, "mixed"); err == nil {
		t.Fatal("mixed Work Callers resolved to one")
	}
}

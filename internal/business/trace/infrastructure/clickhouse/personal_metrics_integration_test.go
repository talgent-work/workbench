//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// A person's usage covers every Work they own, whatever its Work Caller, and
// equals the sum of those Works' own usage, each read under its own Work
// Caller, which its measurements carry.
func TestGetByPersonMetrics_SumsThePersonsWorks(t *testing.T) {
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
	window := query.TraceMetricOptions{StartTime: base, EndTime: base.Add(10 * time.Minute), StepSeconds: 600, MetricKinds: []string{"tool_call"}}

	personal, err := repo.GetByPerson(ctx, "user-1", window)
	if err != nil {
		t.Fatalf("GetByPerson: %v", err)
	}
	if len(personal.Totals) != 1 || personal.Totals[0].Value != 8 {
		t.Fatalf("personal totals = %+v, want the 3 calls of work-1 and the 5 of personal-1", personal.Totals)
	}
	perWork := map[string]float64{}
	for _, bucket := range personal.Buckets {
		perWork[bucket.AgentWorkID] += bucket.Value
	}
	team, owner := query.WorkCaller{Kind: "team", ID: "team-1"}, query.PersonCaller("user-1")
	for work, caller := range map[string]query.WorkCaller{"work-1": team, "personal-1": owner} {
		scoped := window
		scoped.WorkCaller, scoped.AgentWorkID = caller, work
		single, err := repo.GetBySession(ctx, scoped)
		if err != nil {
			t.Fatalf("GetBySession %s: %v", work, err)
		}
		if len(single.Totals) != 1 || single.Totals[0].Value != perWork[work] {
			t.Fatalf("%s totals = %+v, want the personal share %v", work, single.Totals, perWork[work])
		}
	}
	narrowed := window
	narrowed.AgentWorkID = "personal-1"
	one, err := repo.GetByPerson(ctx, "user-1", narrowed)
	if err != nil || len(one.Totals) != 1 || one.Totals[0].Value != 5 {
		t.Fatalf("drill-down = %+v, %v", one, err)
	}
	none, err := repo.GetByPerson(ctx, "", window)
	if err != nil || len(none.Buckets) != 0 {
		t.Fatalf("an empty person read %+v, %v", none, err)
	}
	leaked, err := repo.GetBySession(ctx, query.TraceMetricOptions{WorkCaller: team, AgentWorkID: "personal-1"})
	if err != nil || len(leaked.Buckets) != 0 {
		t.Fatalf("a Standalone Work read under another Work Caller: %+v, %v", leaked, err)
	}
	for work, want := range map[string]query.WorkCaller{"work-1": team, "personal-1": owner} {
		if caller, observed, err := repo.GetWorkCaller(ctx, work); err != nil || !observed || caller != want {
			t.Fatalf("Work Caller of %s = %+v %v %v, want %+v", work, caller, observed, err, want)
		}
	}
	if _, observed, err := repo.GetWorkCaller(ctx, "unmeasured"); err != nil || observed {
		t.Fatalf("an unmeasured Work has a Work Caller: %v %v", observed, err)
	}
}

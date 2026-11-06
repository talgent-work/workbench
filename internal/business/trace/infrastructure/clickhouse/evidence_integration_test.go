//go:build integration

package clickhouse

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// Injected records exercise delivery histories, not native Harness capabilities.
// A late span must refine the existing call, even after a previous day's bucket
// was read. OTLP map iteration order must not change evidence identity or pages.
func TestLateEvidenceAndReorderedReplay(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().Add(-48 * time.Hour).Truncate(time.Minute)
	resources := "map('talgent.project_id','project-late','talgent.agent_work_id','session-late')"
	attrs := "map('event.name','tool_decision','event.sequence','42','session.id','native-session','tool_name','Agent','tool_use_id','call-late')"
	insertLog := "INSERT INTO otel_logs (Timestamp, TraceId, SpanId, Body, LogAttributes, ResourceAttributes) SELECT ?, 'trace-late', '', 'claude_code.tool_decision', " + attrs + ", " + resources
	if err := repo.db.Exec(ctx, insertLog, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	opts := query.TraceMetricOptions{ProjectID: "project-late", StartTime: at, EndTime: at.Add(time.Minute)}
	assertCounts := func(wantDimension string) {
		t.Helper()
		metrics, err := repo.GetByProject(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		var calls float64
		for _, bucket := range metrics.Buckets {
			if bucket.MetricKind == "agent_call" {
				calls += bucket.Value
				if bucket.Dimension != wantDimension {
					t.Fatalf("dimension=%q want %q", bucket.Dimension, wantDimension)
				}
			}
		}
		if calls != 1 {
			t.Fatalf("late/replayed call counted %v times", calls)
		}
	}
	assertCounts("Unknown agent type")
	spanAttrs := "map('tool_name','Agent','tool_use_id','call-late','subagent_type','general-purpose')"
	if err := repo.db.Exec(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes) SELECT ?, 'trace-late', 'span-late', 'claude_code.tool', "+spanAttrs+", "+resources, at); err != nil {
		t.Fatal(err)
	}
	assertCounts("general-purpose")
	// Replay the same records with reversed map serialization order.
	if err := repo.db.Exec(ctx, "INSERT INTO otel_logs (Timestamp, TraceId, SpanId, Body, LogAttributes, ResourceAttributes) SELECT Timestamp, TraceId, SpanId, Body, mapFromArrays(arrayReverse(mapKeys(LogAttributes)), arrayReverse(mapValues(LogAttributes))), mapFromArrays(arrayReverse(mapKeys(ResourceAttributes)), arrayReverse(mapValues(ResourceAttributes))) FROM otel_logs"); err != nil {
		t.Fatal(err)
	}
	if err := repo.db.Exec(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes) SELECT Timestamp, TraceId, SpanId, SpanName, mapFromArrays(arrayReverse(mapKeys(SpanAttributes)), arrayReverse(mapValues(SpanAttributes))), mapFromArrays(arrayReverse(mapKeys(ResourceAttributes)), arrayReverse(mapValues(ResourceAttributes))) FROM otel_traces"); err != nil {
		t.Fatal(err)
	}
	assertCounts("general-purpose")
	t.Run("event detail deduplicates map order", func(t *testing.T) {
		page, err := repo.ListEvents(ctx, query.EventOptions{ProjectID: "project-late", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Events) != 1 || page.NextCursor != "" {
			t.Fatalf("replayed event created a second page: %+v", page)
		}
	})
	t.Run("span detail deduplicates map order", func(t *testing.T) {
		page, err := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "project-late", AgentWorkID: "session-late", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Spans) != 1 || page.NextCursor != "" {
			t.Fatalf("replayed span created a second page: %+v", page)
		}
	})
}

func TestMetricWindowFiltersEvidenceBeforeBucketing(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	start := time.Now().Add(-time.Hour).Truncate(time.Minute).Add(10*time.Second + 123456789*time.Nanosecond)
	end := start.Add(30 * time.Second)
	for i, at := range []time.Time{start.Add(-time.Nanosecond), start, end.Add(-time.Nanosecond), end} {
		attrs := fmt.Sprintf("map('model','fixture','input_tokens','%d','output_tokens','0','cache_read_tokens','0','cache_creation_tokens','0')", (i+1)*10)
		if err := repo.db.Exec(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes) SELECT fromUnixTimestamp64Nano(?), 'trace-window', ?, 'claude_code.llm_request', "+attrs+", map('talgent.project_id','project-window','talgent.agent_work_id','session-window')", at.UnixNano(), fmt.Sprint(i)); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := repo.GetByProject(ctx, query.TraceMetricOptions{ProjectID: "project-window", StartTime: start, EndTime: end, StepSeconds: 15})
	if err != nil {
		t.Fatal(err)
	}
	var calls, input float64
	for _, bucket := range metrics.Buckets {
		if !bucket.BucketStart.Equal(start) && !bucket.BucketStart.Equal(start.Add(15*time.Second)) {
			t.Fatalf("bucket not anchored to query window: %+v", bucket)
		}
		if bucket.MetricKind == "model_call" {
			calls += bucket.Value
		}
		if bucket.MetricKind == "input_tokens" {
			input += bucket.Value
		}
	}
	if calls != 2 || input != 50 {
		t.Fatalf("window included outside evidence or dropped inside evidence: calls=%v input=%v", calls, input)
	}
}

// OTEL SpanId is unique within TraceId, not globally. Equal timestamps and
// SpanIds in different traces must remain separate across a cursor boundary.
func TestSpanIdentityPagination(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	at := time.Now().Add(-time.Minute)
	for _, trace := range []string{"trace-a", "trace-b"} {
		if err := repo.db.Exec(ctx, "INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, ResourceAttributes) SELECT ?, ?, 'same-span', 'claude_code.interaction', map('talgent.project_id','project-pages','talgent.agent_work_id','session-pages')", at, trace); err != nil {
			t.Fatal(err)
		}
	}
	opts := query.SpanListOptions{ProjectID: "project-pages", AgentWorkID: "session-pages", Limit: 1}
	first, err := repo.ListBySession(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Spans) != 1 || first.NextCursor == "" {
		t.Fatalf("first page = %+v", first)
	}
	opts.AfterCursor = first.NextCursor
	second, err := repo.ListBySession(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Spans) != 1 || second.Spans[0].TraceID == first.Spans[0].TraceID || second.NextCursor != "" {
		t.Fatalf("second trace skipped or repeated: %+v", second)
	}
	for _, trace := range []string{"trace-a", "trace-b"} {
		span, err := repo.GetByIdentity(ctx, query.SpanIdentity{ProjectID: "project-pages", TraceID: trace, SpanID: "same-span"})
		if err != nil || span == nil || span.TraceID != trace {
			t.Fatalf("native lookup returned wrong trace: %+v, %v", span, err)
		}
	}
	span, err := repo.GetByIdentity(ctx, query.SpanIdentity{ProjectID: "other-project", TraceID: "trace-a", SpanID: "same-span"})
	if err != nil || span != nil {
		t.Fatalf("native lookup crossed Project scope: %+v, %v", span, err)
	}
}

// Injected legal records cover missing names and producer-supplied content
// markers. This does not claim the pinned CLI emits every possible marker.
func TestUnnamedSkillAndNativeContentMarkers(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"probe", ""} {
		err := repo.db.Exec(ctx, `INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes)
            SELECT now64(9), 'trace-content', ?, 'claude_code.tool',
                map('tool_name','Skill','tool_use_id',?,'skill_name',?,'tool_input','<REDACTED>','tool_output','preview…','tool_output_truncated','true'),
                map('talgent.project_id','project-content','talgent.agent_work_id','session-content')`, "span-"+name, "call-"+name, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := repo.GetByProject(ctx, query.TraceMetricOptions{ProjectID: "project-content"})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, total := range metrics.Totals {
		if total.MetricKind != "skill_call" {
			t.Fatalf("Skill was double classified: %+v", total)
		}
		counts[total.Dimension] += total.Value
	}
	if counts["probe"] != 1 || counts["Unknown skill"] != 1 || len(counts) != 2 {
		t.Fatalf("missing Skill name dropped or merged: %v", counts)
	}
	span, err := repo.GetByIdentity(ctx, query.SpanIdentity{ProjectID: "project-content", TraceID: "trace-content", SpanID: "span-"})
	if err != nil || span == nil {
		t.Fatalf("unnamed native Skill unavailable: %+v, %v", span, err)
	}
	if span.Attributes["tool_input"] != "<REDACTED>" || span.Attributes["tool_output"] != "preview…" || span.Attributes["tool_output_truncated"] != "true" {
		t.Fatalf("native content markers lost: %+v", span.Attributes)
	}
	if _, present := span.Attributes["uncollected_detail"]; present {
		t.Fatal("missing native detail was fabricated")
	}
}

// A caller selecting known product metrics must still see evidence that the
// classification is incomplete, without pulling in unrelated healthy metrics.
func TestMetricFiltersPreserveClassificationGaps(t *testing.T) {
	dsn, cleanup := startClickHouse(t)
	defer cleanup()
	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, record := range []struct{ span, name, attributes string }{
		{"unknown", "claude_code.tool", "map('tool_use_id','unknown-call')"},
		{"healthy", "claude_code.tool", "map('tool_use_id','known-call','tool_name','Read')"},
		{"model", "claude_code.llm_request", "map('model','fixture','input_tokens','10','output_tokens','5','cache_read_tokens','0','cache_creation_tokens','0')"},
	} {
		if err := repo.db.Exec(ctx, `INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes)
            SELECT now64(9), 'trace-filter', ?, ?, `+record.attributes+`, map('talgent.project_id','project-filter','talgent.agent_work_id','session-filter')`, record.span, record.name); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []struct {
		name string
		read func(context.Context, query.TraceMetricOptions) (*query.TraceMetricResult, error)
	}{
		{"Project", repo.GetByProject}, {"Session", repo.GetBySession},
	} {
		t.Run(scope.name, func(t *testing.T) {
			metrics, err := scope.read(ctx, query.TraceMetricOptions{ProjectID: "project-filter", AgentWorkID: "session-filter", MetricKinds: []string{"agent_call"}})
			if err != nil {
				t.Fatal(err)
			}
			var unclassified float64
			var classificationGap bool
			for _, bucket := range metrics.Buckets {
				if bucket.MetricKind != "unclassified_call" {
					t.Fatalf("filter leaked unrelated healthy metric: %+v", bucket)
				}
				unclassified += bucket.Value
				for _, gap := range bucket.Gaps {
					classificationGap = classificationGap || gap == "unclassified_call"
				}
			}
			if unclassified != 1 || !classificationGap {
				t.Fatalf("filtered metrics hid uncertain Agent classification: %+v", metrics)
			}
		})
	}

	t.Run("log-only call", func(t *testing.T) {
		if err := repo.db.Exec(ctx, `INSERT INTO otel_logs (Timestamp, TraceId, Body, LogAttributes, ResourceAttributes)
            SELECT now64(9), 'trace-log-filter', 'claude_code.tool_decision',
                map('event.name','tool_decision','tool_use_id','unknown-call','event.sequence','1'),
                map('talgent.project_id','project-log-filter','talgent.agent_work_id','session-log-filter')`); err != nil {
			t.Fatal(err)
		}
		metrics, err := repo.GetByProject(ctx, query.TraceMetricOptions{ProjectID: "project-log-filter", MetricKinds: []string{"agent_call"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(metrics.Buckets) != 1 || metrics.Buckets[0].MetricKind != "unclassified_call" || metrics.Buckets[0].Value != 1 || !slices.Contains(metrics.Buckets[0].Gaps, "unclassified_call") {
			t.Fatalf("log-only classification gap hidden: %+v", metrics)
		}
	})
}

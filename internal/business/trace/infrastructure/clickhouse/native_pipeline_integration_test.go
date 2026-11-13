//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
)

// Explicit native-record injection covers faults and ages that a live CLI
// cannot deterministically produce, so these cases need no CLI and run with
// the other integration cases. They are not evidence of CLI capability.
func TestInjectedEvidenceRetentionAndFaults(t *testing.T) {
	dsn := middlewaretest.ClickHouse(t, "talgent_trace")
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ddl, err := os.ReadFile("../../../../../deploy/middleware/init/clickhouse/002-talgent.sql")
	if err != nil {
		t.Fatal(err)
	}
	t.Run("injected unknown usage and retention", func(t *testing.T) {
		// Explicit native-record injection covers faults/ages that a live CLI
		// cannot deterministically produce. It is not evidence of CLI capability.
		for _, item := range []struct {
			id  string
			age int
		}{{"recent", 0}, {"retained-metrics", 8}, {"expired-metrics", 31}} {
			_, err := db.ExecContext(ctx, `INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, ResourceAttributes, SpanAttributes)
                SELECT now64(9) - toIntervalDay(?), 'injected-trace', ?, 'claude_code.llm_request',
                    map('talgent.work_caller', 'team:team-edge', 'talgent.agent_work_id', ?),
                    map('model', 'fixture-model', 'input_tokens', 'invalid', 'output_tokens', '0', 'cache_read_tokens', '-1')`, item.age, item.id, item.id)
			if err != nil {
				t.Fatal(err)
			}
		}
		repo, err := openTestRepository(t, dsn)
		if err != nil {
			t.Fatal(err)
		}
		metrics := repo
		for _, id := range []string{"recent", "retained-metrics"} {
			result, err := metrics.GetBySession(ctx, query.TraceMetricOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-edge"}, AgentWorkID: id})
			if err != nil {
				t.Fatal(err)
			}
			totals := map[string]query.TraceMetricTotal{}
			for _, total := range result.Totals {
				totals[total.MetricKind] = total
			}
			if len(totals) != 5 || totals["model_call"].Value != 1 {
				t.Fatalf("missing usage categories: %+v", totals)
			}
			// Unrecorded or invalid usage, such as on a rejected request, is zero.
			for _, kind := range []string{"input_tokens", "output_tokens", "cache_read_tokens", "cache_creation_tokens"} {
				if totals[kind].Value != 0 || totals[kind].UnknownCount != 0 {
					t.Fatalf("token usage must count unrecorded values as zero: %+v", totals[kind])
				}
			}
		}
		recentSpans, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-edge"}, AgentWorkID: "recent"})
		if err != nil || len(recentSpans.Spans) != 1 {
			t.Fatalf("raw expiry boundary: %+v, %v", recentSpans, err)
		}
		oldSpans, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-edge"}, AgentWorkID: "retained-metrics"})
		if err != nil || len(oldSpans.Spans) != 0 {
			t.Fatalf("raw expiry boundary: %+v, %v", oldSpans, err)
		}
		expired, err := metrics.GetBySession(ctx, query.TraceMetricOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-edge"}, AgentWorkID: "expired-metrics"})
		if err != nil || len(expired.Totals) != 0 {
			t.Fatalf("expired measurements still readable: %+v, %v", expired, err)
		}
	})
	t.Run("physical evidence expiry preserves retained measurements", func(t *testing.T) {
		// Insert old but valid evidence through the same raw tables and MVs.
		// Explicit TTL materialization makes the actual storage deletion bounded.
		if _, err := db.ExecContext(ctx, `INSERT INTO otel_logs (Timestamp, TraceId, Body, LogAttributes, ResourceAttributes)
            SELECT now64(9) - INTERVAL 8 DAY, 'ttl-trace', 'claude_code.skill_activated',
                map('event.name', 'skill_activated', 'event.sequence', '1', 'skill.name', 'retained-skill'),
                map('talgent.work_caller', 'team:team-ttl', 'talgent.agent_work_id', 'session-ttl')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes)
            SELECT now64(9) - INTERVAL 8 DAY, 'ttl-trace', 'ttl-span', 'claude_code.llm_request',
                map('model', 'ttl-model', 'input_tokens', '10', 'output_tokens', '5', 'cache_read_tokens', '0', 'cache_creation_tokens', '0'),
                map('talgent.work_caller', 'team:team-ttl', 'talgent.agent_work_id', 'session-ttl')`); err != nil {
			t.Fatal(err)
		}
		repo, err := openTestRepository(t, dsn)
		if err != nil {
			t.Fatal(err)
		}
		opts := query.TraceMetricOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-ttl"}, AgentWorkID: "session-ttl"}
		before, err := repo.GetBySession(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []string{"otel_traces", "otel_logs", "otel_traces_trace_id_ts", "trace_work_measurements"} {
			if _, err := db.ExecContext(ctx, "ALTER TABLE "+table+" MATERIALIZE TTL SETTINGS mutations_sync=2"); err != nil {
				t.Fatal(err)
			}
		}
		for _, table := range []string{"otel_traces", "otel_logs", "otel_traces_trace_id_ts"} {
			var count uint64
			if err := db.QueryRowContext(ctx, "SELECT count() FROM "+table+" WHERE TraceId='ttl-trace'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("expired %s still stored: %d %v", table, count, err)
			}
		}
		after, err := repo.GetBySession(ctx, opts)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("evidence expiry changed retained measurements: %v", err)
		}
		totals := map[string]float64{}
		for _, total := range after.Totals {
			totals[total.MetricKind] += total.Value
		}
		if totals["input_tokens"] != 10 || totals["output_tokens"] != 5 || totals["skill_activation"] != 1 {
			t.Fatalf("retained measurements missing: %+v", totals)
		}
		var expired uint64
		if err := db.QueryRowContext(ctx, "SELECT count() FROM trace_work_measurements WHERE timestamp < now() - INTERVAL 30 DAY").Scan(&expired); err != nil || expired != 0 {
			t.Fatalf("expired metrics still stored: %d %v", expired, err)
		}
	})
	t.Run("interpretation failure retains evidence and reports a gap", func(t *testing.T) {
		// A deliberately broken span interpreter must not discard accepted OTLP
		// evidence, suppress independent log measurements, or look like zero usage.
		brokenDDL := string(ddl)
		marker := "CREATE MATERIALIZED VIEW talgent_trace.trace_spans_measurements_mv"
		start := strings.Index(brokenDDL, marker)
		end := start + strings.Index(brokenDDL[start:], ";")
		brokenDDL = brokenDDL[:end] + " AND throwIf(SpanName = 'claude_code.llm_request', 'injected interpreter failure') = 0" + brokenDDL[end:]
		applyNativeSchema(t, ctx, db, brokenDDL)
		t.Cleanup(func() { applyNativeSchema(t, ctx, db, string(ddl)) })

		now := time.Now()
		resource := `{"attributes":[{"key":"talgent.work_caller","value":{"stringValue":"team:team-fault"}},{"key":"talgent.work_key","value":{"stringValue":"board:board-fault"}},{"key":"talgent.agent_work_id","value":{"stringValue":"session-fault"}}]}`
		spanBody := fmt.Sprintf(`{"resourceSpans":[{"resource":%s,"scopeSpans":[{"spans":[{"traceId":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","spanId":"bbbbbbbbbbbbbbbb","name":"claude_code.llm_request","startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"model","value":{"stringValue":"fixture-model"}},{"key":"input_tokens","value":{"stringValue":"5"}},{"key":"output_tokens","value":{"stringValue":"0"}},{"key":"cache_read_tokens","value":{"stringValue":"0"}},{"key":"cache_creation_tokens","value":{"stringValue":"0"}}]}]}]}]}`, resource, now.UnixNano(), now.Add(time.Millisecond).UnixNano())
		logBody := fmt.Sprintf(`{"resourceLogs":[{"resource":%s,"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","body":{"stringValue":"claude_code.skill_activated"},"attributes":[{"key":"event.name","value":{"stringValue":"skill_activated"}},{"key":"event.sequence","value":{"stringValue":"1"}},{"key":"skill.name","value":{"stringValue":"probe"}}]}]}]}]}`, resource, now.UnixNano())
		client := &http.Client{Timeout: 5 * time.Second}
		for signal, body := range map[string]string{"traces": spanBody, "logs": logBody} {
			response, err := client.Post(middlewaretest.Environment(t, "TALGENT_TEST_OTLP_URL")+"/v1/"+signal, "application/json", strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("OTLP %s: %s", signal, response.Status)
			}
		}
		repo, err := openTestRepository(t, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer repo.db.Close()
		// Measurements, spans and events carry the Work Key the resource names.
		faultKey := query.WorkKey{Kind: "board", ID: "board-fault"}
		options := query.TraceMetricOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-fault"}, AgentWorkID: "session-fault", MetricKinds: []string{"model_call", "input_tokens", "skill_activation"}}
		var result *query.TraceMetricResult
		for deadline := time.Now().Add(10 * time.Second); ; {
			result, err = repo.GetBySession(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			var activation, gap bool
			for _, bucket := range result.Buckets {
				activation = activation || (bucket.MetricKind == "skill_activation" && bucket.Value == 1 && bucket.WorkKey == faultKey)
				gap = gap || slices.Contains(bucket.Gaps, "projection_incomplete")
			}
			if activation && gap {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("independent activation and known projection gap absent: %+v", result)
			}
			time.Sleep(100 * time.Millisecond)
		}
		spans, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-fault"}, AgentWorkID: "session-fault"})
		if err != nil || len(spans.Spans) != 1 || spans.Spans[0].Attributes["input_tokens"] != "5" || spans.Spans[0].WorkKey != faultKey {
			t.Fatalf("native evidence lost after interpretation failure: %+v, %v", spans, err)
		}
		events, err := repo.ListEvents(ctx, query.EventOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-fault"}, AgentWorkID: "session-fault"})
		if err != nil || len(events.Events) != 1 || events.Events[0].WorkKey != faultKey {
			t.Fatalf("independent native event lost: %+v, %v", events, err)
		}

	})
}

func applyNativeSchema(t *testing.T, ctx context.Context, db *sql.DB, ddl string) {
	t.Helper()
	for _, statement := range strings.Split(ddl, ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
}

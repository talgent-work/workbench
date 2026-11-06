//go:build integration

package clickhouse

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tracepublicv1 "github.com/talgent-work/workbench/gen/go/talgent/trace/public/v1"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"

	"connectrpc.com/connect"
	"github.com/talgent-work/workbench/internal/business/trace/application"
	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	tracerpc "github.com/talgent-work/workbench/internal/business/trace/transport/connectrpc"
	"github.com/talgent-work/workbench/pkg/authn"
	"github.com/talgent-work/workbench/pkg/nativetest"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Native CLI -> official OTLP collector -> ClickHouse -> production repository.
// Only the model response is a fixture; no spans or metric rows are fabricated.
func TestNativeClaudeCollectorPipeline(t *testing.T) {
	nativetest.Require(t, "CLAUDE_BINARY")
	dsn := middlewaretest.ClickHouse(t, "talgent_trace")
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for _, scenario := range []struct{ scope, action string }{{"work", "read"}, {"employee", "read"}, {"work", "agent"}, {"work", "nested_agent"}, {"work", "skill"}, {"work", "slash_skill"}, {"work", "mcp"}, {"work", "mcp_connect_fail"}, {"work", "mcp_disconnect"}, {"work", "details_off"}, {"work", "content_on"}, {"work", "interrupted"}, {"work", "tool_fail"}, {"work", "denied"}, {"work", "skill_invalid"}, {"work", "mcp_fail"}} {
		scope := scenario.scope
		t.Run(scope+"/"+scenario.action, func(t *testing.T) {
			sessionID := "session-probe"
			if scope == "employee" {
				sessionID = "employee-session-probe"
			}
			if scenario.action != "read" {
				sessionID += "-" + scenario.action
			}
			command := exec.CommandContext(ctx, "python3", "../../../../../scripts/probes/claude_native_telemetry.py")
			command.Env = append(os.Environ(), "NATIVE_TRACE_SCOPE="+scope, "NATIVE_PROBE_SCENARIO="+scenario.action, "NATIVE_OTLP_ENDPOINT="+middlewaretest.Environment(t, "TALGENT_TEST_OTLP_URL"))
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("native probe: %v\n%s", err, output)
			}
			t.Log(string(output))
			var probe struct {
				ModelRequests            int    `json:"model_requests"`
				InterruptedModelRequests int    `json:"interrupted_model_requests"`
				Evidence                 string `json:"evidence"`
			}
			if err := json.Unmarshal(output, &probe); err != nil {
				t.Fatal(err)
			}
			repo, err := openTestRepository(t, dsn)
			if err != nil {
				t.Fatal(err)
			}
			minimumSpans := 7
			if scenario.action == "denied" {
				minimumSpans = 6
			}
			if scenario.action == "skill_invalid" {
				minimumSpans = 4
			}
			// The pinned runtime omits the interrupted streaming request itself.
			// Count only exported evidence, never reconstruct the missing request.
			expectedModels := probe.ModelRequests - probe.InterruptedModelRequests
			if scenario.action == "slash_skill" || scenario.action == "mcp_connect_fail" || scenario.action == "interrupted" {
				minimumSpans = expectedModels + 1
			}
			var spans []*query.Span
			for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
				page, queryErr := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "project-probe", AgentWorkID: sessionID, Limit: 100})
				err = queryErr
				if page != nil {
					spans = page.Spans
				}
				if err != nil {
					t.Fatal(err)
				}
				observedModels := 0
				for _, span := range spans {
					if span.Name == "claude_code.llm_request" {
						observedModels++
					}
				}
				if len(spans) >= minimumSpans && observedModels == expectedModels {
					break
				}
				time.Sleep(200 * time.Millisecond)
			}
			if len(spans) < minimumSpans {
				t.Fatalf("native spans = %d, want >= %d", len(spans), minimumSpans)
			}
			tools, agents, skills, mcps, models, childModels := 0, 0, 0, 0, 0, 0
			callsByID := map[string]bool{}
			for _, span := range spans {
				if span.AgentWorkID != sessionID {
					t.Fatalf("native attribution lost: %+v", span)
				}
				if scope == "work" && (span.OwnerContext != "case" || span.OwnerID != "case-probe") {
					t.Fatalf("caller attributes lost: %+v", span)
				}
				if _, exists := span.Attributes["talgent.work_id"]; exists {
					t.Fatal("obsolete Work telemetry identity")
				}
				if scope == "employee" && (span.AgentID != "employee-agent-probe" || span.ResourceAttributes["talgent.user_id"] != "") {
					t.Fatalf("employee attribution lost or presented as human: %+v", span)
				}
				if span.Name == "claude_code.tool" {
					callsByID[span.TraceID+":"+span.CallID] = true
					if span.Category == "agent_call" {
						agents++
					} else if span.Category == "tool_call" {
						tools++
					} else if span.Category == "skill_call" {
						skills++
					} else if span.Category == "mcp_call" {
						mcps++
					} else {
						t.Fatalf("unexpected category: %+v", span)
					}
				}
				if span.Name == "claude_code.llm_request" {
					models++
					if span.NativeAgentID != "" {
						childModels++
					}
				}
			}
			if models != expectedModels {
				t.Fatalf("model count=%d, provider requests=%d, interrupted=%d", models, probe.ModelRequests, probe.InterruptedModelRequests)
			}
			if scenario.action == "agent" || scenario.action == "nested_agent" {
				wantAgents := 1
				if scenario.action == "nested_agent" {
					wantAgents = 2
				}
				if agents != wantAgents || childModels < wantAgents {
					t.Fatalf("delegation=%d child models=%d", agents, childModels)
				}
			}
			isolated, err := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "another-project", AgentWorkID: sessionID, Limit: 100})
			if err != nil || len(isolated.Spans) != 0 {
				t.Fatalf("cross-project session leak: %d, %v", len(isolated.Spans), err)
			}

			metricRepo, err := openTestRepository(t, dsn)
			if err != nil {
				t.Fatal(err)
			}
			app := tracerpc.NewHandler(application.NewApplication(query.NewQueries(repo, metricRepo, repo, nativePipelineAuthorizer{}, nativePipelineAuthorizer{})))
			allowed := authn.WithAuthenticatedUser(ctx, authn.AuthenticatedUser{ID: "reader"})
			response, err := app.ListSpansBySession(allowed, connect.NewRequest(&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-probe", AgentWorkId: sessionID}))
			if err != nil || len(response.Msg.Spans) != len(spans) {
				t.Fatalf("authorized native API query failed: %v", err)
			}
			_, err = app.ListSpansBySession(authn.WithAuthenticatedUser(ctx, authn.AuthenticatedUser{ID: "stranger"}), connect.NewRequest(&tracepublicv1.ListSpansBySessionRequest{ProjectId: "project-probe", AgentWorkId: sessionID}))
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("native API authorization bypass: %v", err)
			}
			expectedResults := tools + agents + skills + mcps
			if scenario.action == "denied" {
				expectedResults = 0
			}
			expectedEvents := tools + agents + skills + mcps + expectedResults
			if scenario.action == "skill" || scenario.action == "slash_skill" || strings.HasPrefix(scenario.action, "mcp") {
				expectedEvents++
			}
			if scenario.action == "mcp_disconnect" {
				expectedEvents++
			}
			var events *connect.Response[tracepublicv1.ListEventsResponse]
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
				events, err = app.ListEvents(allowed, connect.NewRequest(&tracepublicv1.ListEventsRequest{ProjectId: "project-probe", AgentWorkId: sessionID}))
				if err != nil {
					t.Fatal(err)
				}
				if len(events.Msg.Events) >= expectedEvents {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			// ClickHouse fills each materialized view in turn, so evidence can be
			// visible with only its receipt. Read metrics once every receipt has
			// been superseded by its interpretation.
			var metrics *query.TraceMetricResult
			for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
				metrics, err = metricRepo.GetBySession(ctx, query.TraceMetricOptions{ProjectID: "project-probe", AgentWorkID: sessionID, StartTime: time.Now().Add(-time.Hour), EndTime: time.Now().Add(time.Hour), StepSeconds: 60})
				if err != nil {
					t.Fatal(err)
				}
				if !slices.ContainsFunc(metrics.Buckets, func(bucket query.TraceMetricBucket) bool {
					return slices.Contains(bucket.Gaps, "projection_incomplete")
				}) || time.Now().After(deadline) {
					break
				}
			}
			totals := map[string]float64{}
			for _, total := range metrics.Totals {
				totals[total.MetricKind] += total.Value
			}
			expected := map[string]float64{"tool_call": 0, "agent_call": 0, "skill_call": 0, "mcp_call": 0, "skill_activation": 0, "model_call": float64(models)}
			switch scenario.action {
			case "read", "tool_fail", "denied", "details_off", "content_on":
				expected["tool_call"] = 1
			case "agent":
				expected["tool_call"], expected["agent_call"] = 1, 1
			case "nested_agent":
				expected["tool_call"], expected["agent_call"] = 1, 2
			case "skill":
				expected["skill_call"], expected["skill_activation"] = 1, 1
			case "slash_skill":
				expected["skill_activation"] = 1
			case "mcp", "mcp_fail", "mcp_disconnect":
				expected["mcp_call"] = 1
			}
			for kind, want := range expected {
				if totals[kind] != want {
					t.Fatalf("%s=%v, want %v", kind, totals[kind], want)
				}
			}
			for kind, perRequest := range map[string]int{"input_tokens": 10, "output_tokens": 5, "cache_read_tokens": 7, "cache_creation_tokens": 11} {
				if totals[kind] != float64(models*perRequest) {
					t.Fatalf("native %s total=%v, want %d", kind, totals[kind], models*perRequest)
				}
				for _, total := range metrics.Totals {
					if total.MetricKind == kind && total.UnknownCount != 0 {
						t.Fatalf("reported native usage marked unknown: %+v", total)
					}
				}
			}
			sessionMetrics, err := metricRepo.GetBySession(ctx, query.TraceMetricOptions{ProjectID: "project-probe", AgentWorkID: sessionID, StartTime: time.Now().Add(-time.Hour), EndTime: time.Now().Add(time.Hour), StepSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			sessionTotals := map[string]float64{}
			for _, total := range sessionMetrics.Totals {
				sessionTotals[total.MetricKind] += total.Value
			}
			if sessionTotals["input_tokens"] != float64(models*10) || sessionTotals["tool_call"] != float64(tools) || sessionTotals["agent_call"] != float64(agents) {
				t.Fatalf("Session metrics omitted runtime purpose %s: %v", scope, sessionTotals)
			}
			for _, bucket := range sessionMetrics.Buckets {
				if bucket.AgentWorkID != sessionID {
					t.Fatalf("mixed Session metrics: %+v", bucket)
				}
			}

			// Event content is the CLI's own; scope, Collector admission and call
			// association are this pipeline's mapping.
			for _, event := range events.Msg.Events {
				if event.ResourceAttributes["talgent.agent_work_id"] != sessionID {
					t.Fatalf("event scope lost: %+v", event)
				}
				switch event.Body {
				case "claude_code.tool_decision", "claude_code.tool_result":
				case "claude_code.skill_activated", "claude_code.mcp_server_connection":
					continue
				default:
					t.Fatalf("unrelated log admitted: %s", event.Body)
				}
				if event.Attributes["tool_use_id"] == "" || !callsByID[event.TraceId+":"+event.Attributes["tool_use_id"]] {
					t.Fatalf("call association lost: %+v", event)
				}
			}
			deniedEvents, err := app.ListEvents(authn.WithAuthenticatedUser(ctx, authn.AuthenticatedUser{ID: "stranger"}), connect.NewRequest(&tracepublicv1.ListEventsRequest{ProjectId: "project-probe", AgentWorkId: sessionID}))
			if connect.CodeOf(err) != connect.CodePermissionDenied || deniedEvents != nil {
				t.Fatalf("native event access bypass: %v", err)
			}
			// A transport retry must not change metrics; observe via the production repository.
			before := metrics
			if _, err := db.ExecContext(ctx, "INSERT INTO otel_traces SELECT * FROM otel_traces WHERE ResourceAttributes['talgent.agent_work_id'] = ?", sessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO otel_logs SELECT * FROM otel_logs WHERE ResourceAttributes['talgent.agent_work_id'] = ?", sessionID); err != nil {
				t.Fatal(err)
			}
			after, err := metricRepo.GetBySession(ctx, query.TraceMetricOptions{ProjectID: "project-probe", AgentWorkID: sessionID, StartTime: time.Now().Add(-time.Hour), EndTime: time.Now().Add(time.Hour), StepSeconds: 60})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before.Totals, after.Totals) {
				t.Fatalf("replay changed totals: before=%+v after=%+v", before.Totals, after.Totals)
			}
			metricResponse, err := app.GetSessionTraceMetrics(allowed, connect.NewRequest(&tracepublicv1.GetSessionTraceMetricsRequest{ProjectId: "project-probe", AgentWorkId: sessionID}))
			if err != nil {
				t.Fatal(err)
			}
			for name, evidence := range map[string]proto.Message{"trace-query": response.Msg, "event-query": events.Msg, "metric-query": metricResponse.Msg} {
				encoded, err := (protojson.MarshalOptions{Indent: "  "}).Marshal(evidence)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(probe.Evidence, name+".json"), encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Logf("native pipeline PASS: %d spans, %d tool calls, %d Agent calls, %d Skill calls, %d MCP calls, %d model calls; resource scope preserved", len(spans), tools, agents, skills, mcps, models)
		})
	}
}

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
                    map('talgent.project_id', 'project-edge', 'talgent.agent_work_id', ?),
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
			result, err := metrics.GetBySession(ctx, query.TraceMetricOptions{ProjectID: "project-edge", AgentWorkID: id})
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
		recentSpans, err := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "project-edge", AgentWorkID: "recent"})
		if err != nil || len(recentSpans.Spans) != 1 {
			t.Fatalf("raw expiry boundary: %+v, %v", recentSpans, err)
		}
		oldSpans, err := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "project-edge", AgentWorkID: "retained-metrics"})
		if err != nil || len(oldSpans.Spans) != 0 {
			t.Fatalf("raw expiry boundary: %+v, %v", oldSpans, err)
		}
		expired, err := metrics.GetBySession(ctx, query.TraceMetricOptions{ProjectID: "project-edge", AgentWorkID: "expired-metrics"})
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
                map('talgent.project_id', 'project-ttl', 'talgent.agent_work_id', 'session-ttl')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO otel_traces (Timestamp, TraceId, SpanId, SpanName, SpanAttributes, ResourceAttributes)
            SELECT now64(9) - INTERVAL 8 DAY, 'ttl-trace', 'ttl-span', 'claude_code.llm_request',
                map('model', 'ttl-model', 'input_tokens', '10', 'output_tokens', '5', 'cache_read_tokens', '0', 'cache_creation_tokens', '0'),
                map('talgent.project_id', 'project-ttl', 'talgent.agent_work_id', 'session-ttl')`); err != nil {
			t.Fatal(err)
		}
		repo, err := openTestRepository(t, dsn)
		if err != nil {
			t.Fatal(err)
		}
		opts := query.TraceMetricOptions{ProjectID: "project-ttl", AgentWorkID: "session-ttl"}
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
		resource := `{"attributes":[{"key":"talgent.project_id","value":{"stringValue":"project-fault"}},{"key":"talgent.agent_work_id","value":{"stringValue":"session-fault"}}]}`
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
		options := query.TraceMetricOptions{ProjectID: "project-fault", AgentWorkID: "session-fault", MetricKinds: []string{"model_call", "input_tokens", "skill_activation"}}
		var result *query.TraceMetricResult
		for deadline := time.Now().Add(10 * time.Second); ; {
			result, err = repo.GetBySession(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			var activation, gap bool
			for _, bucket := range result.Buckets {
				activation = activation || (bucket.MetricKind == "skill_activation" && bucket.Value == 1)
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
		spans, err := repo.ListBySession(ctx, query.SpanListOptions{ProjectID: "project-fault", AgentWorkID: "session-fault"})
		if err != nil || len(spans.Spans) != 1 || spans.Spans[0].Attributes["input_tokens"] != "5" {
			t.Fatalf("native evidence lost after interpretation failure: %+v, %v", spans, err)
		}
		events, err := repo.ListEvents(ctx, query.EventOptions{ProjectID: "project-fault", AgentWorkID: "session-fault"})
		if err != nil || len(events.Events) != 1 {
			t.Fatalf("independent native event lost: %+v, %v", events, err)
		}

	})
}

type nativePipelineAuthorizer struct{}

func (nativePipelineAuthorizer) AuthorizeProjectReader(_ context.Context, actor, project string) error {
	if actor == "reader" && project == "project-probe" {
		return nil
	}
	return query.ErrAuthorizationPermissionDenied
}

func (nativePipelineAuthorizer) AuthorizeWorkReader(context.Context, string, string) error {
	return query.ErrAuthorizationPermissionDenied
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

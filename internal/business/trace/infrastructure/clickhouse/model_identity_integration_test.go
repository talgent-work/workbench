//go:build integration

package clickhouse

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/business/trace/application/query"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// Intent: the Collector stores one name per model. A Harness context-window
// suffix such as [1m] is removed from spans and logs at reception, while every
// other attribute is kept exactly as received.
func TestCollectorNormalizesModelIdentity(t *testing.T) {
	dsn := middlewaretest.ClickHouse(t, "talgent_trace")
	endpoint := middlewaretest.Environment(t, "TALGENT_TEST_OTLP_URL")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	work := fmt.Sprintf("work-model-identity-%d", time.Now().UnixNano())
	now := time.Now().UnixNano()
	resource := fmt.Sprintf(`{"attributes":[{"key":"talgent.work_caller","value":{"stringValue":"team:team-model"}},{"key":"talgent.agent_work_id","value":{"stringValue":%q}}]}`, work)
	post := func(path, body string) {
		t.Helper()
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+path, bytes.NewBufferString(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("OTLP %s status = %d", path, response.StatusCode)
		}
	}
	post("/v1/traces", fmt.Sprintf(`{"resourceSpans":[{"resource":%s,"scopeSpans":[{"spans":[{"traceId":"0102030405060708090a0b0c0d0e0f10","spanId":"0102030405060708","name":"claude_code.llm_request","startTimeUnixNano":"%d","endTimeUnixNano":"%d","attributes":[{"key":"model","value":{"stringValue":"deepseek-v4.1-flash[1m]"}},{"key":"gen_ai.request.model","value":{"stringValue":"glm-5[1m]"}},{"key":"query_source","value":{"stringValue":"repl[1m]"}}]}]}]}]}`, resource, now, now))
	post("/v1/logs", fmt.Sprintf(`{"resourceLogs":[{"resource":%s,"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","body":{"stringValue":"claude_code.tool_result"},"attributes":[{"key":"model","value":{"stringValue":"qwen3.8-max[1m]"}}]}]}]}]}`, resource, now))

	repo, err := openTestRepository(t, dsn)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("clickhouse", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var span *query.Span
	var logModel string
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline) && (span == nil || logModel == ""); time.Sleep(200 * time.Millisecond) {
		page, err := repo.ListBySession(ctx, query.SpanListOptions{WorkCaller: query.WorkCaller{Kind: "team", ID: "team-model"}, AgentWorkID: work, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Spans) > 0 {
			span = page.Spans[0]
		}
		row := db.QueryRowContext(ctx, "SELECT LogAttributes['model'] FROM otel_logs WHERE ResourceAttributes['talgent.agent_work_id'] = ? LIMIT 1", work)
		if err := row.Scan(&logModel); err != nil && err != sql.ErrNoRows {
			t.Fatal(err)
		}
	}
	if span == nil || logModel == "" {
		t.Fatalf("evidence not stored: span=%v log model=%q", span, logModel)
	}
	if got := span.Attributes["model"]; got != "deepseek-v4.1-flash" {
		t.Fatalf("span model = %q", got)
	}
	if got := span.Attributes["gen_ai.request.model"]; got != "glm-5" {
		t.Fatalf("span gen_ai.request.model = %q", got)
	}
	if got := span.Attributes["query_source"]; got != "repl[1m]" {
		t.Fatalf("unrelated attribute changed: %q", got)
	}
	if logModel != "qwen3.8-max" {
		t.Fatalf("log model = %q", logModel)
	}
}

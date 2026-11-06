//go:build integration

package telemetryingest

import (
	"bytes"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/stretchr/testify/require"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
)

// Managed telemetry -> this endpoint -> the official Collector -> ClickHouse:
// what a machine attributed to another Work and person is stored, and
// measured for Usage, under its own run's Work, person and Computer.
func TestManagedTelemetryLandsUnderItsRunsAttribution(t *testing.T) {
	dsn := middlewaretest.ClickHouse(t, "talgent_trace")
	collector, err := NewCollector(http.DefaultClient, middlewaretest.Environment(t, "TALGENT_TEST_OTLP_URL"))
	require.NoError(t, err)
	mux := http.NewServeMux()
	Register(mux, runs{"work-a": "token-a"}, collector, slog.New(slog.DiscardHandler))
	server := httptest.NewServer(mux)
	defer server.Close()

	now := uint64(time.Now().UnixNano())
	body, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource: forgedResource(),
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
			Name: "claude_code.llm_request", TraceId: bytes.Repeat([]byte{7}, 16), SpanId: bytes.Repeat([]byte{8}, 8),
			StartTimeUnixNano: now - uint64(time.Second), EndTimeUnixNano: now,
			Attributes: []*commonpb.KeyValue{stringAttribute("model", "claude-fixture"), {Key: "input_tokens", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: 12}}}},
		}}}},
	}}})
	require.NoError(t, err)
	response := post(t, server.URL+Prefix+"/v1/traces", body, map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a"})
	require.Equal(t, http.StatusOK, response.StatusCode)

	db, err := sql.Open("clickhouse", dsn)
	require.NoError(t, err)
	defer db.Close()
	var work, person, computer, name string
	require.Eventually(t, func() bool {
		row := db.QueryRowContext(t.Context(), "SELECT ResourceAttributes['talgent.agent_work_id'], ResourceAttributes['talgent.user_id'], ResourceAttributes['talgent.computer_id'], ResourceAttributes['talgent.computer_name'] FROM otel_traces WHERE SpanName = 'claude_code.llm_request' LIMIT 1")
		return row.Scan(&work, &person, &computer, &name) == nil
	}, 30*time.Second, 250*time.Millisecond)
	require.Equal(t, []string{"work-a", "person-work-a", "computer-1", "Studio Mac"}, []string{work, person, computer, name})
	var measured, forged int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT countIf(agent_work_id = 'work-a' AND user_id = 'person-work-a'), countIf(user_id = 'someone-else' OR agent_work_id = 'work-b') FROM trace_work_measurements").Scan(&measured, &forged))
	require.Positive(t, measured, "Usage measures it for the run's person")
	require.Zero(t, forged)
}

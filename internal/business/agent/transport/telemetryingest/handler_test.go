package telemetryingest

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

// runs issues one telemetry token per Work's live run.
type runs map[string]string

func (r runs) RunTelemetryAttribution(_ context.Context, workID, token string) (map[string]string, error) {
	if issued, ok := r[workID]; !ok || issued != token || token == "" {
		return nil, query.ErrTelemetryTokenRejected
	}
	return map[string]string{"talgent.agent_work_id": workID, "talgent.user_id": "person-" + workID, "talgent.computer_id": "computer-1", "talgent.computer_name": "Studio Mac"}, nil
}

// collectorFixture is the OTel Collector, recording what reached it.
type collectorFixture struct {
	mu       sync.Mutex
	status   int
	received map[string][][]byte
}

func (c *collectorFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.received[r.URL.Path] = append(c.received[r.URL.Path], body)
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
}

func newEndpoint(t *testing.T) (*httptest.Server, *collectorFixture) {
	t.Helper()
	fixture := &collectorFixture{received: map[string][][]byte{}}
	upstream := httptest.NewServer(fixture)
	t.Cleanup(upstream.Close)
	collector, err := NewCollector(upstream.Client(), upstream.URL)
	require.NoError(t, err)
	mux := http.NewServeMux()
	Register(mux, runs{"work-a": "token-a", "work-b": "token-b"}, collector, slog.New(slog.DiscardHandler))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, fixture
}

func stringAttribute(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// forgedResource is what a machine claims for itself: another Work, person
// and Computer, beside the process's own attributes.
func forgedResource() *resourcepb.Resource {
	return &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		stringAttribute("service.name", "claude-code"),
		stringAttribute("talgent.agent_work_id", "work-b"),
		stringAttribute("talgent.user_id", "someone-else"),
		stringAttribute("talgent.project_id", "project-x"),
		stringAttribute("talgent.computer_id", "forged"),
	}}
}

func traces(t *testing.T) []byte {
	t.Helper()
	body, err := proto.Marshal(&coltracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{
		{Resource: forgedResource(), ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "claude_code.llm_request", TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8)}}}}},
		{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "claude_code.tool"}}}}},
	}})
	require.NoError(t, err)
	return body
}

func post(t *testing.T, url string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/x-protobuf")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func resourceOf(resource *resourcepb.Resource) map[string]string {
	attributes := map[string]string{}
	for _, attribute := range resource.GetAttributes() {
		attributes[attribute.GetKey()] = attribute.GetValue().GetStringValue()
	}
	return attributes
}

// Whatever a machine attributes its telemetry to, what reaches the Collector
// carries its run's attribution in the platform's namespace, on every
// resource, and keeps the process's own attributes and the telemetry itself.
func TestForgedAttributionIsReplacedByTheRunsOwn(t *testing.T) {
	server, collector := newEndpoint(t)
	response := post(t, server.URL+Prefix+"/v1/traces", traces(t), map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a"})
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Len(t, collector.received["/v1/traces"], 1)
	var forwarded coltracepb.ExportTraceServiceRequest
	require.NoError(t, proto.Unmarshal(collector.received["/v1/traces"][0], &forwarded))
	require.Len(t, forwarded.GetResourceSpans(), 2)
	own := map[string]string{"talgent.agent_work_id": "work-a", "talgent.user_id": "person-work-a", "talgent.computer_id": "computer-1", "talgent.computer_name": "Studio Mac"}
	first := resourceOf(forwarded.GetResourceSpans()[0].GetResource())
	require.Equal(t, "claude-code", first["service.name"])
	delete(first, "service.name")
	require.Equal(t, own, first, "talgent.project_id, which the run does not have, is dropped too")
	require.Equal(t, own, resourceOf(forwarded.GetResourceSpans()[1].GetResource()), "a resource the machine left out is attributed as well")
	require.Equal(t, "claude_code.llm_request", forwarded.GetResourceSpans()[0].GetScopeSpans()[0].GetSpans()[0].GetName())

	var gzipped bytes.Buffer
	writer := gzip.NewWriter(&gzipped)
	logs, err := proto.Marshal(&collogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{Resource: forgedResource(), ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "claude_code.tool_result"}}}}}}}}})
	require.NoError(t, err)
	_, err = writer.Write(logs)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	response = post(t, server.URL+Prefix+"/v1/logs", gzipped.Bytes(), map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a", "Content-Encoding": "gzip"})
	require.Equal(t, http.StatusOK, response.StatusCode)
	var forwardedLogs collogspb.ExportLogsServiceRequest
	require.NoError(t, proto.Unmarshal(collector.received["/v1/logs"][0], &forwardedLogs))
	logResource := resourceOf(forwardedLogs.GetResourceLogs()[0].GetResource())
	delete(logResource, "service.name")
	require.Equal(t, own, logResource)
}

// Only the run's own token writes its telemetry: another Work's, a forged or
// a missing one is refused before anything reaches the Collector.
func TestATokenWritesOnlyItsOwnRunsTelemetry(t *testing.T) {
	server, collector := newEndpoint(t)
	for name, headers := range map[string]map[string]string{
		"another Work's token":       {"Authorization": "Bearer token-b", "X-Talgent-Work-Id": "work-a"},
		"its token for another Work": {"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-b"},
		"forged":                     {"Authorization": "Bearer forged", "X-Talgent-Work-Id": "work-a"},
		"no Work":                    {"Authorization": "Bearer token-a"},
		"no token":                   {"X-Talgent-Work-Id": "work-a"},
		"not a bearer":               {"Authorization": "token-a", "X-Talgent-Work-Id": "work-a"},
	} {
		response := post(t, server.URL+Prefix+"/v1/traces", traces(t), headers)
		require.Equal(t, http.StatusUnauthorized, response.StatusCode, name)
	}
	require.Empty(t, collector.received)
}

// Each request is bounded and only OTLP protobuf is taken; a malformed one is
// refused, so it is never retried.
func TestTheEndpointTakesOnlyBoundedOTLPProtobuf(t *testing.T) {
	server, collector := newEndpoint(t)
	auth := map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a"}
	response := post(t, server.URL+Prefix+"/v1/traces", []byte("not protobuf \xff"), auth)
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	response = post(t, server.URL+Prefix+"/v1/traces", make([]byte, maxWireBytes+1), auth)
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	var bomb bytes.Buffer
	writer := gzip.NewWriter(&bomb)
	_, _ = writer.Write(make([]byte, maxDecodedBytes+1))
	require.NoError(t, writer.Close())
	response = post(t, server.URL+Prefix+"/v1/traces", bomb.Bytes(), map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a", "Content-Encoding": "gzip"})
	require.Equal(t, http.StatusRequestEntityTooLarge, response.StatusCode)
	request, err := http.NewRequest(http.MethodPost, server.URL+Prefix+"/v1/traces", bytes.NewReader(traces(t)))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	for name, value := range auth {
		request.Header.Set(name, value)
	}
	json, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	_ = json.Body.Close()
	require.Equal(t, http.StatusUnsupportedMediaType, json.StatusCode)
	response = post(t, server.URL+Prefix+"/v1/metrics", traces(t), auth)
	require.Equal(t, http.StatusNotFound, response.StatusCode, "the Harness exports no metrics")
	require.Empty(t, collector.received)
}

// The exporter retries what the Collector could not take now, and only that.
func TestCollectorAnswersReachTheExporter(t *testing.T) {
	server, collector := newEndpoint(t)
	auth := map[string]string{"Authorization": "Bearer token-a", "X-Talgent-Work-Id": "work-a"}
	for upstream, answered := range map[int]int{http.StatusServiceUnavailable: http.StatusServiceUnavailable, http.StatusTooManyRequests: http.StatusTooManyRequests, http.StatusBadRequest: http.StatusBadRequest, http.StatusInternalServerError: http.StatusBadGateway} {
		collector.mu.Lock()
		collector.status = upstream
		collector.mu.Unlock()
		response := post(t, server.URL+Prefix+"/v1/traces", traces(t), auth)
		require.Equal(t, answered, response.StatusCode, "Collector %d", upstream)
	}
}

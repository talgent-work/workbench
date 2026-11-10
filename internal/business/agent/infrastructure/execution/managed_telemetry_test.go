package execution

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// A Managed run reports the same telemetry as a Cloud run: its environment
// holds the same switches and attribution, and the start's trace context;
// the endpoint and the run's own token reach the machine beside it, for
// Talgent Computer to give the Harness, and neither the token nor the Work
// credential is in it.
func TestAManagedRunReportsTelemetryLikeACloudRun(t *testing.T) {
	relay := &relayFixture{}
	server := httptest.NewServer(relay)
	defer server.Close()
	machines, err := NewManagedComputers(server.Client(), server.URL, ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: managedTelemetryAddress}, "control", &managedPlace{})
	require.NoError(t, err)
	environment := ManagedEnvironment{machines: machines, models: fixtureModels()}
	input := managedRequest()
	input.ComputerName = managedComputerName
	input.TraceAttributes = map[string]string{"talgent.agent_work_id": input.WorkID}
	traceID, err := trace.TraceIDFromHex("12345678901234567890123456789012")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("1234567890123456")
	require.NoError(t, err)
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))

	specified, err := environment.specify(ctx, input, testMounts, owner(input), "execd-token")
	require.NoError(t, err)
	spec, err := decodeManagedSpecification(executionRecord{SpecificationJSON: specified.document})
	require.NoError(t, err)
	require.NotEmpty(t, spec.Telemetry.Token)
	require.NoError(t, machines.open(ctx, spec, "session"))

	env := relay.environment["environment"].(map[string]any)
	for name, value := range map[string]string{
		"CLAUDE_CODE_ENABLE_TELEMETRY": "1", "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1", "OTEL_TRACES_EXPORTER": "otlp", "OTEL_LOGS_EXPORTER": "otlp",
		"OTEL_METRICS_EXPORTER": "none", "OTEL_LOG_TOOL_DETAILS": "1", "OTEL_LOG_TOOL_CONTENT": "0", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
		"OTEL_RESOURCE_ATTRIBUTES": "talgent.agent_work_id=session,talgent.computer_id=" + managedComputerID + ",talgent.computer_name=Studio%20Mac%2C%202",
		"TRACEPARENT":              "00-12345678901234567890123456789012-1234567890123456-01",
	} {
		require.Equal(t, value, env[name], name)
	}
	require.NotContains(t, env, "OTEL_EXPORTER_OTLP_ENDPOINT", "Talgent Computer sets where the telemetry goes")
	require.NotContains(t, env, "OTEL_EXPORTER_OTLP_HEADERS")
	require.Equal(t, map[string]any{"endpoint": managedTelemetryAddress, "token": spec.Telemetry.Token}, relay.environment["telemetry"])
	encoded, err := json.Marshal(env)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), spec.Telemetry.Token)
	require.Equal(t, map[string]string{"talgent.agent_work_id": "session", "talgent.computer_id": managedComputerID, "talgent.computer_name": managedComputerName}, spec.Telemetry.Attributes)

	again, err := environment.specify(ctx, input, testMounts, owner(input), "execd-token")
	require.NoError(t, err)
	next, err := decodeManagedSpecification(executionRecord{SpecificationJSON: again.document})
	require.NoError(t, err)
	require.NotEqual(t, spec.Telemetry.Token, next.Telemetry.Token, "each run is issued its own token")
}

// Talgent Computer sends its telemetry token over the internet only.
func TestManagedComputersRequireAnHTTPSTelemetryAddress(t *testing.T) {
	for _, address := range []string{"", "http://workbench.example/computer/v1/otel", "https://user@workbench.example/otel", "https://workbench.example/otel?x=1"} {
		_, err := NewManagedComputers(http.DefaultClient, "http://relay.invalid", ManagedAddresses{Gateway: managedGatewayAddress, Content: managedContentAddress, Telemetry: address}, "control", &managedPlace{})
		require.Error(t, err, address)
	}
}

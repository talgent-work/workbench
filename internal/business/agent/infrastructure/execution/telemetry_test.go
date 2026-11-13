package execution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

func TestNativeLaunchTelemetryExportsWorkAttribution(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	input.TraceAttributes = map[string]string{
		"talgent.work_caller": "project:project-1", "talgent.agent_work_id": input.WorkID,
		"talgent.work_key": "review:review-1", "domain.case_id": "case-1",
	}
	traceID, err := trace.TraceIDFromHex("12345678901234567890123456789012")
	require.NoError(t, err)
	spanID, err := trace.SpanIDFromHex("1234567890123456")
	require.NoError(t, err)
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	spec, err := provider.specification(ctx, input, testMounts, owner(input), "test-token")
	require.NoError(t, err)
	require.Equal(t, "00-12345678901234567890123456789012-1234567890123456-01", spec.Env["TRACEPARENT"])
	require.NotContains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.execution_id", "telemetry is attributed per Work, never per process run")
	require.Contains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.agent_work_id="+input.WorkID)
	require.Contains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.work_caller=project:project-1")
	require.Contains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.work_key=review:review-1")
	require.Contains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "domain.case_id=case-1")
	require.Equal(t, "http/protobuf", spec.Env["OTEL_EXPORTER_OTLP_PROTOCOL"])
	require.Equal(t, "1", spec.Env["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"])
	require.NotContains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.computer_", "a start without a Computer names none")
	input.TraceAttributes["talgent.work_key"] = "review:a b,talgent.user_id=another-user;一"
	spec, err = provider.specification(ctx, input, testMounts, owner(input), "test-token")
	require.NoError(t, err, "an opaque identity is carried, encoded")
	require.Contains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.work_key=review:a%20b%2Ctalgent.user_id%3Danother-user%3B%E4%B8%80")
	require.NotContains(t, spec.Env["OTEL_RESOURCE_ATTRIBUTES"], "talgent.user_id=another-user", "a delimiter in a value never injects an attribute")
	input.TraceAttributes = map[string]string{"bad,key": "x"}
	_, err = provider.specification(ctx, input, testMounts, owner(input), "test-token")
	require.Error(t, err, "a key that cannot be carried unchanged rejects the launch")
}

// A Cloud run's telemetry names its Computer as a Managed run's does: its id
// and its name, percent-encoded as Claude Code decodes it, which no caller
// attribute replaces.
func TestCloudTelemetryNamesItsComputer(t *testing.T) {
	provider, _, _ := sandboxClient(t)
	input := request()
	input.ComputerID, input.ComputerName = "cloud-1", "Talgent Cloud, 一"
	input.TraceAttributes = map[string]string{"talgent.agent_work_id": input.WorkID, "talgent.computer_id": "forged", "talgent.computer_name": "forged"}
	spec, err := provider.specification(context.Background(), input, testMounts, owner(input), "test-token")
	require.NoError(t, err)
	require.Equal(t, "talgent.agent_work_id="+input.WorkID+",talgent.computer_id=cloud-1,talgent.computer_name=Talgent%20Cloud%2C%20%E4%B8%80", spec.Env["OTEL_RESOURCE_ATTRIBUTES"])
}

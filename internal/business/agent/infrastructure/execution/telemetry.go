package execution

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/propagation"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Resource attributes that name the Computer a run's telemetry came from, on
// a Cloud and a Managed Computer alike.
const (
	computerIDAttribute   = "talgent.computer_id"
	computerNameAttribute = "talgent.computer_name"
)

// telemetryAttribution is the resource attribution of a run's Harness
// telemetry: the attributes AgentWork supplies, its common attribution and
// caller extensions, beside the Computer the run is on, which no caller
// attribute replaces. Telemetry is attributed per Work and Computer, never
// per process run. A caller attribute that could not be carried unchanged in
// OTEL_RESOURCE_ATTRIBUTES rejects the start; the Computer's name, a person's
// choice, is percent-encoded instead (resourceAttributes).
func telemetryAttribution(request domain.StartExecutionRequest) (map[string]string, error) {
	attributes := make(map[string]string, len(request.TraceAttributes)+2)
	for key, value := range request.TraceAttributes {
		if value == "" {
			continue
		}
		for _, part := range []string{key, value} {
			for _, char := range part {
				if char <= 32 || char > 126 || strings.ContainsRune(",;\\\"", char) {
					return nil, fmt.Errorf("%w: invalid telemetry attribution", domain.ErrExecutionRequest)
				}
			}
		}
		if strings.Contains(key, "=") {
			return nil, domain.ErrExecutionRequest
		}
		attributes[key] = value
	}
	delete(attributes, computerIDAttribute)
	delete(attributes, computerNameAttribute)
	if id := strings.TrimSpace(request.ComputerID); id != "" {
		attributes[computerIDAttribute] = id
	}
	if name := strings.TrimSpace(request.ComputerName); name != "" {
		attributes[computerNameAttribute] = name
	}
	return attributes, nil
}

// resourceAttributes spells attributes as OTEL_RESOURCE_ATTRIBUTES, sorted by
// key, each value percent-encoded wherever it is not a plain identifier
// character; Claude Code decodes them.
func resourceAttributes(attributes map[string]string) string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+percentEncode(attributes[key]))
	}
	return strings.Join(pairs, ",")
}

func percentEncode(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("-._~:/@", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// telemetryEnvironment sets, in env, how a run's Claude Code reports its
// telemetry, the same on every Computer: traces and log events over OTLP
// HTTP/protobuf with the run's resource attribution, tool details but not
// tool content unless env already chooses otherwise, and the start's trace
// context when it has one. Where the telemetry goes is the realization's.
func telemetryEnvironment(ctx context.Context, env map[string]string, attributes map[string]string) {
	env["CLAUDE_CODE_ENABLE_TELEMETRY"] = "1"
	env["CLAUDE_CODE_ENHANCED_TELEMETRY_BETA"] = "1"
	env["OTEL_TRACES_EXPORTER"] = "otlp"
	env["OTEL_LOGS_EXPORTER"] = "otlp"
	env["OTEL_METRICS_EXPORTER"] = "none"
	if _, configured := env["OTEL_LOG_TOOL_DETAILS"]; !configured {
		env["OTEL_LOG_TOOL_DETAILS"] = "1"
	}
	if _, configured := env["OTEL_LOG_TOOL_CONTENT"]; !configured {
		env["OTEL_LOG_TOOL_CONTENT"] = "0"
	}
	env["OTEL_EXPORTER_OTLP_PROTOCOL"] = "http/protobuf"
	env["OTEL_RESOURCE_ATTRIBUTES"] = resourceAttributes(attributes)
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	delete(env, "TRACEPARENT")
	delete(env, "TRACESTATE")
	if parent := carrier.Get("traceparent"); parent != "" {
		env["TRACEPARENT"] = parent
	}
	if state := carrier.Get("tracestate"); state != "" {
		env["TRACESTATE"] = state
	}
}

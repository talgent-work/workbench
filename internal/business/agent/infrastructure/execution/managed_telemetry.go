package execution

import (
	"context"
	"crypto/subtle"
	"maps"
	"strings"

	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// A Managed run's Claude Code reports the same telemetry a Cloud run's does
// (telemetryEnvironment), straight to the platform's public telemetry
// endpoint. It
// authenticates with a telemetry token of its own, issued with the run's
// execution and revoked with it, which writes only that run's telemetry:
// never the Work's Identity Token or a model key, which no process of the Work
// holds. The endpoint replaces the resource attribution of everything sent
// with the one recorded here, so a machine cannot attribute telemetry to
// another Work, person or Computer.

// managedTelemetry is a Managed execution's telemetry, recorded in its
// specification. Endpoint and Token reach the machine in the environment
// PUT, never Environment; Attributes stay with the platform.
type managedTelemetry struct {
	Endpoint   string            `json:"endpoint"`
	Token      string            `json:"token"`
	Attributes map[string]string `json:"attributes"`
}

// attachTelemetry gives the run's Harness its telemetry: the same
// environment as on a Cloud, and the endpoint and token Talgent Computer
// adds as OTEL_EXPORTER_OTLP_ENDPOINT and OTEL_EXPORTER_OTLP_HEADERS.
func (s *managedSpecification) attachTelemetry(ctx context.Context, request domain.LaunchSpecification, endpoint, token string) error {
	if strings.TrimSpace(endpoint) == "" || strings.TrimSpace(token) == "" {
		return domain.ErrExecutionRequest
	}
	attributes, err := telemetryAttribution(request)
	if err != nil {
		return err
	}
	telemetryEnvironment(ctx, s.Environment, attributes)
	s.Telemetry = managedTelemetry{Endpoint: endpoint, Token: token, Attributes: attributes}
	return nil
}

// machineTelemetry is the telemetry of the environment PUT.
type machineTelemetry struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
}

func (s managedSpecification) machineTelemetry() machineTelemetry {
	return machineTelemetry{Endpoint: s.Telemetry.Endpoint, Token: s.Telemetry.Token}
}

// RunTelemetryAttribution authenticates a Managed run's telemetry by its
// token, against the Work's current live execution, and answers the
// attribution of that run. Any other token, one of an earlier run, another
// Work's or a Cloud run's, which has none, is rejected alike.
func (q *ExecutionQueryRepository) RunTelemetryAttribution(ctx context.Context, workID, token string) (map[string]string, error) {
	if strings.TrimSpace(workID) == "" || strings.TrimSpace(token) == "" {
		return nil, query.ErrTelemetryTokenRejected
	}
	record, found, err := q.records.live(ctx, workID)
	if err != nil {
		return nil, err
	}
	if !found || record.Realization != realizationManaged {
		return nil, query.ErrTelemetryTokenRejected
	}
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return nil, err
	}
	issued := spec.Telemetry
	if subtle.ConstantTimeCompare([]byte(issued.Token), []byte(token)) != 1 {
		return nil, query.ErrTelemetryTokenRejected
	}
	return maps.Clone(issued.Attributes), nil
}

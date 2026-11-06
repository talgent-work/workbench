package query

import (
	"context"
	"errors"
)

// RunTelemetryAttributions authenticates the telemetry a Managed Work's run
// sends by the telemetry token execution infrastructure issued to the Work's
// current live allocation, and answers the resource attribution everything
// that run sends carries, which replaces whatever the machine attributed
// itself. The token writes only that run's telemetry: it is revoked with the
// allocation and grants no other authority. AgentWork takes no part.
type RunTelemetryAttributions interface {
	RunTelemetryAttribution(ctx context.Context, workID, token string) (map[string]string, error)
}

// ErrTelemetryTokenRejected means the token is not the one issued to the
// Work's current live run.
var ErrTelemetryTokenRejected = errors.New("agent: telemetry token rejected")

package query

import "errors"

// ErrTelemetryTokenRejected means the token is not the one issued to the
// Work's current live run.
var ErrTelemetryTokenRejected = errors.New("agent: telemetry token rejected")

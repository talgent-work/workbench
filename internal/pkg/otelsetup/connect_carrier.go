package otelsetup

import (
	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/propagation"
)

// ConnectCarrier carries trace context in a Connect call's metadata, as
// propagation.HeaderCarrier does in an HTTP header.
type ConnectCarrier struct {
	*connect.Header
}

var _ propagation.TextMapCarrier = ConnectCarrier{}

// Keys lists the metadata's keys.
func (c ConnectCarrier) Keys() []string {
	keys := make([]string, 0, c.Len())
	for key := range c.All() {
		keys = append(keys, key)
	}
	return keys
}

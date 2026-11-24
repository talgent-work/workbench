package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/propagation"
)

// CallContext is the context of exactly one outbound call whose request
// headers set writes. Connect v2 keeps a call's headers on its context, and
// a later call on the same context reuses them, so derive one per call and
// never call on it twice.
func CallContext(ctx context.Context, set func(header *connect.Header)) context.Context {
	ctx, info := connect.NewClientContext(ctx)
	set(info.RequestHeader())
	return ctx
}

// TracedCallContext is CallContext that also carries ctx's W3C trace
// context, for a service whose spans join the caller's trace.
func TracedCallContext(ctx context.Context, set func(header *connect.Header)) context.Context {
	return CallContext(ctx, func(header *connect.Header) {
		propagation.TraceContext{}.Inject(ctx, headerCarrier{header: header})
		set(header)
	})
}

// CallerTraceContext is ctx joined to the W3C trace context the served call's
// caller sent, if any.
func CallerTraceContext(ctx context.Context) context.Context {
	return propagation.TraceContext{}.Extract(ctx, headerCarrier{header: RequestHeader(ctx)})
}

// RequestHeader is the request headers of the call ctx serves; outside a
// served call, such as a handler called directly, it is empty.
func RequestHeader(ctx context.Context) *connect.Header {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		return info.RequestHeader()
	}
	return &connect.Header{}
}

// headerCarrier carries a trace context in connect request headers.
type headerCarrier struct {
	header *connect.Header
}

func (c headerCarrier) Get(key string) string { return c.header.Get(key) }

func (c headerCarrier) Set(key, value string) { c.header.Set(key, value) }

func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, c.header.Len())
	for key := range c.header.All() {
		keys = append(keys, key)
	}
	return keys
}

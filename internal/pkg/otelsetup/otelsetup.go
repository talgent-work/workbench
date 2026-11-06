// Package otelsetup wires the OpenTelemetry SDK for Talgent services that emit
// spans via OTLP. It is the single place OTEL SDK setup lives so every
// service (Executor today, any future emitter tomorrow) shares the same
// env-driven configuration.
//
// Honored environment variables follow standard OTEL SDK conventions:
//
//   - OTEL_SDK_DISABLED=true installs a no-op shutdown and returns
//     immediately. The default OTEL tracer is already a no-op, so no global
//     provider change is needed. This lets tests and local dev run without a
//     live collector.
//   - OTEL_EXPORTER_OTLP_ENDPOINT is consumed by otlptracegrpc.NewClient()
//     automatically; we never pass it explicitly.
//   - OTEL_SERVICE_NAME, if set, overrides the serviceName parameter.
//
// OTEL_EXPORTER_OTLP_PROTOCOL is ignored: this helper is gRPC-only. HTTP can
// be added later if a service needs it.
package otelsetup

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ShutdownFunc flushes and closes the TracerProvider. Callers MUST invoke
// the returned shutdown at process exit to flush the last batch.
type ShutdownFunc func(ctx context.Context) error

// Init configures the global OTEL TracerProvider (gRPC/OTLP exporter) and
// returns a shutdown func. Callers MUST invoke the returned shutdown at
// process exit to flush the last batch.
func Init(ctx context.Context, serviceName string) (ShutdownFunc, error) {
	if isSDKDisabled() {
		return func(context.Context) error { return nil }, nil
	}

	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		serviceName = name
	}

	// otlptracegrpc consumes OTEL_EXPORTER_OTLP_ENDPOINT internally but
	// silently logs and ignores parse failures, which would surface as
	// missing telemetry hours later. Validate eagerly so misconfig fails
	// fast at Init.
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		if _, err := url.Parse(endpoint); err != nil {
			return nil, fmt.Errorf("otelsetup: invalid OTEL_EXPORTER_OTLP_ENDPOINT %q: %w", endpoint, err)
		}
	}

	client := otlptracegrpc.NewClient()
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("otelsetup: create OTLP exporter: %w", err)
	}

	res, err := serviceResource(serviceName)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, fmt.Errorf("otelsetup: merge resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(2*time.Second)),
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp.Shutdown, nil
}

// serviceResource names the service on top of the SDK's default resource.
// The name carries no schema URL: the default resource declares the SDK's
// own schema, and two different schema URLs cannot be merged.
func serviceResource(serviceName string) (*sdkresource.Resource, error) {
	return sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(semconv.ServiceName(serviceName)))
}

func isSDKDisabled() bool {
	return strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true")
}

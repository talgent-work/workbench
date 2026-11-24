// Package telemetry provides the process's OpenTelemetry MeterProvider,
// configured by the standard OTEL_* environment. The services record metrics
// only and start no spans.
package telemetry

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.uber.org/fx"
)

// metricInterval is how often metrics are collected and exported.
const metricInterval = time.Minute

// Module provides the MeterProvider of the service named serviceName and
// flushes it on stop. It is listed before the bounded contexts, so it shuts
// down after every instrument that records through it.
func Module(serviceName string) fx.Option {
	return fx.Module("internal.pkg.telemetry",
		fx.Provide(func(lc fx.Lifecycle) (metric.MeterProvider, error) {
			provider, shutdown, err := newMeterProvider(context.Background(), serviceName)
			if err != nil {
				return nil, err
			}
			lc.Append(fx.StopHook(shutdown))
			return provider, nil
		}),
		fx.Invoke(func(metric.MeterProvider) {}),
	)
}

// newMeterProvider exports metrics over OTLP/HTTP to the collector that
// OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_METRICS_ENDPOINT names.
// Without either, or with OTEL_SDK_DISABLED=true, metrics are recorded
// nowhere. OTEL_SERVICE_NAME overrides serviceName.
func newMeterProvider(ctx context.Context, serviceName string) (metric.MeterProvider, func(context.Context) error, error) {
	if strings.EqualFold(os.Getenv("OTEL_SDK_DISABLED"), "true") ||
		(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "") {
		return noop.NewMeterProvider(), func(context.Context) error { return nil }, nil
	}
	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		serviceName = name
	}
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: create OTLP metric exporter: %w", err)
	}
	// The name carries no schema URL: the default resource declares the SDK's
	// own schema, and two different schema URLs cannot be merged.
	resource, err := sdkresource.Merge(sdkresource.Default(), sdkresource.NewSchemaless(semconv.ServiceName(serviceName)))
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, nil, fmt.Errorf("telemetry: merge resource: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(metricInterval))),
		sdkmetric.WithResource(resource),
	)
	return provider, provider.Shutdown, nil
}

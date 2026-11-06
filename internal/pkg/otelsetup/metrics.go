package otelsetup

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// metricInterval is how often metrics are collected and exported.
const metricInterval = time.Minute

// NewMeterProvider exports metrics over OTLP/HTTP to the collector named by
// the standard OTEL_EXPORTER_OTLP_ENDPOINT or
// OTEL_EXPORTER_OTLP_METRICS_ENDPOINT. Without either, or with
// OTEL_SDK_DISABLED=true, metrics are recorded nowhere.
func NewMeterProvider(ctx context.Context, serviceName string) (metric.MeterProvider, ShutdownFunc, error) {
	if isSDKDisabled() || (os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT") == "") {
		return noop.NewMeterProvider(), func(context.Context) error { return nil }, nil
	}
	if name := os.Getenv("OTEL_SERVICE_NAME"); name != "" {
		serviceName = name
	}
	exporter, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("otelsetup: create OTLP metric exporter: %w", err)
	}
	res, err := serviceResource(serviceName)
	if err != nil {
		_ = exporter.Shutdown(ctx)
		return nil, nil, fmt.Errorf("otelsetup: merge resource: %w", err)
	}
	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(metricInterval))),
		sdkmetric.WithResource(res),
	)
	return provider, provider.Shutdown, nil
}

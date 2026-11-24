package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

// Without a collector endpoint, or with the SDK disabled, metrics are
// recorded nowhere rather than failing startup.
func TestMeterProviderRecordsNowhereWithoutACollector(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	provider, shutdown, err := newMeterProvider(context.Background(), "controlplane")
	require.NoError(t, err)
	require.IsType(t, noop.MeterProvider{}, provider)
	require.NoError(t, shutdown(context.Background()))

	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_SDK_DISABLED", "true")
	provider, _, err = newMeterProvider(context.Background(), "controlplane")
	require.NoError(t, err)
	require.IsType(t, noop.MeterProvider{}, provider)
}

// With a collector endpoint the provider exports, and building it does not
// contact the collector; its shutdown flushes.
func TestMeterProviderExportsToTheConfiguredCollector(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	provider, shutdown, err := newMeterProvider(context.Background(), "controlplane")
	require.NoError(t, err)
	_, exports := provider.(interface{ ForceFlush(context.Context) error })
	require.True(t, exports, "provider %T does not export", provider)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

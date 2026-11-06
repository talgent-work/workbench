package otelsetup

import (
	"context"
	"testing"
	"time"
)

// TestInit_NoopWhenDisabled guards that setting OTEL_SDK_DISABLED=true
// short-circuits Init without touching exporters, so tests and local dev
// without a running collector don't crash. Init must still return a non-nil
// ShutdownFunc so callers can defer it unconditionally.
func TestInit_NoopWhenDisabled(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "true")

	shutdown, err := Init(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil ShutdownFunc")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown returned error: %v", err)
	}
}

// TestInit_FailsOnInvalidEndpoint guards that a malformed OTLP endpoint is
// surfaced as an Init error rather than swallowed, preventing silent
// telemetry loss. otlptracegrpc itself logs-and-ignores URL parse failures,
// so Init eagerly validates OTEL_EXPORTER_OTLP_ENDPOINT with url.Parse; a
// value with an invalid percent escape ("%zz") triggers that validation.
func TestInit_FailsOnInvalidEndpoint(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "%zz")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	shutdown, err := Init(ctx, "test-service")
	if err == nil {
		if shutdown != nil {
			_ = shutdown(context.Background())
		}
		t.Fatal("Init succeeded with invalid endpoint, expected error")
	}
}

// Without a collector endpoint, metrics go nowhere rather than failing start.
func TestNewMeterProvider_NoopWithoutEndpoint(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	provider, shutdown, err := NewMeterProvider(context.Background(), "test-service")
	if err != nil || provider == nil || shutdown == nil {
		t.Fatalf("NewMeterProvider = %v, %v", provider, err)
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

// With a collector endpoint, the provider is built; creating it does not
// contact the collector.
func TestNewMeterProvider_ExportsWithEndpoint(t *testing.T) {
	t.Setenv("OTEL_SDK_DISABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	provider, shutdown, err := NewMeterProvider(context.Background(), "test-service")
	if err != nil {
		t.Fatalf("NewMeterProvider: %v", err)
	}
	if _, ok := provider.(interface{ ForceFlush(context.Context) error }); !ok {
		t.Fatalf("provider %T does not export", provider)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

func TestServiceResourceNamesTheService(t *testing.T) {
	res, err := serviceResource("controlplane")
	if err != nil {
		t.Fatalf("serviceResource: %v", err)
	}
	if got, _ := res.Set().Value("service.name"); got.AsString() != "controlplane" {
		t.Fatalf("service.name = %q", got.AsString())
	}
}

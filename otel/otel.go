// Package otel provides OpenTelemetry setup for massrelay.
//
// Two usage modes:
//
// 1. Standalone (massrelay owns OTEL):
//
//	promHandler, shutdown := otel.Setup(ctx)
//	defer shutdown(ctx)
//	metrics := otel.NewMetrics(nil) // uses global provider
//
// 2. Embedded (host app owns OTEL):
//
//	cfg := &otel.Config{MeterProvider: hostMeterProvider}
//	metrics := otel.NewMetrics(cfg.MeterProvider)
//
// Standalone configuration is via standard OTEL env vars:
//
//	OTEL_EXPORTER_OTLP_ENDPOINT  - OTLP collector endpoint (enables OTLP export)
//	OTEL_SERVICE_NAME            - service name (default "massrelay")
//	OTEL_METRICS_PROMETHEUS      - "true" to enable Prometheus /metrics endpoint
//
// If neither OTLP endpoint nor Prometheus is configured, OTEL is a no-op.
package otel

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	otellib "go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.28.0"
)

// Config holds optional providers injected by the host application.
// When massrelay is embedded in another server that already has OTEL set up,
// pass the host's providers here to avoid conflicting global state.
//
// Any nil field falls back to the global provider.
type Config struct {
	MeterProvider metric.MeterProvider
	// TracerProvider trace.TracerProvider  // future: when tracing is added
	// LoggerProvider log.LoggerProvider    // future: when structured logging is added
}

// MeterFrom returns a "massrelay" meter from the given provider.
// If provider is nil, uses the global provider (set by Setup or by the host app).
func MeterFrom(provider metric.MeterProvider) metric.Meter {
	if provider == nil {
		provider = otellib.GetMeterProvider()
	}
	return provider.Meter("massrelay")
}

// Setup initializes OpenTelemetry metrics and sets the global provider.
// Use this when massrelay runs standalone and owns the OTEL lifecycle.
// Call shutdown on server exit to flush pending metrics.
// Returns a non-nil Prometheus HTTP handler if Prometheus export is enabled.
func Setup(ctx context.Context) (promHandler http.Handler, shutdown func(context.Context) error) {
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "massrelay"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		log.Printf("[OTEL] Failed to create resource: %v", err)
		return nil, func(context.Context) error { return nil }
	}

	var opts []sdkmetric.Option
	opts = append(opts, sdkmetric.WithResource(res))

	// OTLP exporter (if endpoint configured)
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		exporter, err := otlpmetrichttp.New(ctx)
		if err != nil {
			log.Printf("[OTEL] Failed to create OTLP exporter: %v", err)
		} else {
			opts = append(opts, sdkmetric.WithReader(
				sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(15*time.Second)),
			))
			log.Printf("[OTEL] OTLP metrics exporter enabled → %s", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
		}
	}

	// Prometheus exporter (if enabled)
	if os.Getenv("OTEL_METRICS_PROMETHEUS") == "true" {
		promExp, err := promexporter.New()
		if err != nil {
			log.Printf("[OTEL] Failed to create Prometheus exporter: %v", err)
		} else {
			opts = append(opts, sdkmetric.WithReader(promExp))
			promHandler = promhttp.Handler()
			log.Println("[OTEL] Prometheus metrics enabled at /metrics")
		}
	}

	// If no exporters configured, OTEL is a no-op (global meter provider is already no-op)
	if len(opts) <= 1 { // only resource, no readers
		log.Println("[OTEL] No exporters configured, metrics disabled")
		return nil, func(context.Context) error { return nil }
	}

	provider := sdkmetric.NewMeterProvider(opts...)
	otellib.SetMeterProvider(provider)

	return promHandler, provider.Shutdown
}

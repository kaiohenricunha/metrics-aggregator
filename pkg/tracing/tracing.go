// Package tracing provides opt-in OpenTelemetry initialization for the metrics-aggregator.
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
)

// InitTracing configures the global OpenTelemetry TracerProvider.
// It reads OTEL_TRACES_EXPORTER to decide the exporter:
//   - "none" or empty: no-op (zero overhead)
//   - "stdout": writes spans to stdout (for development)
//   - "otlp": sends spans via gRPC to OTEL_EXPORTER_OTLP_ENDPOINT
//
// For the "otlp" exporter, TLS is used by default. Set OTEL_EXPORTER_OTLP_INSECURE=true
// to disable TLS (e.g. for local development with a plaintext collector).
//
// Returns a shutdown function that flushes pending spans.
func InitTracing(serviceName string) (shutdown func(context.Context) error, err error) {
	exporter := os.Getenv("OTEL_TRACES_EXPORTER")
	if exporter == "" || exporter == "none" {
		// No-op: keep the default global TracerProvider
		return func(context.Context) error { return nil }, nil
	}

	var spanExporter sdktrace.SpanExporter
	switch exporter {
	case "stdout":
		spanExporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, err
		}
	case "otlp":
		opts := []otlptracegrpc.Option{}
		if os.Getenv("OTEL_EXPORTER_OTLP_INSECURE") == "true" {
			opts = append(opts, otlptracegrpc.WithInsecure())
		}
		spanExporter, err = otlptracegrpc.New(context.Background(), opts...)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported OTEL_TRACES_EXPORTER value %q: must be one of: none, stdout, otlp", exporter)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spanExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	return tp.Shutdown, nil
}

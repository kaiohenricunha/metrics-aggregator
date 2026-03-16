package tracing

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"
)

func TestInitTracing_Disabled(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	shutdown, err := InitTracing("test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer shutdown(context.Background())

	// Should be a no-op provider
	tp := otel.GetTracerProvider()
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "test-span")
	span.End()
	// No-op provider produces spans with invalid SpanContext
	if span.SpanContext().IsValid() {
		t.Fatal("expected no-op span with invalid SpanContext when tracing is disabled")
	}
}

func TestInitTracing_Stdout(t *testing.T) {
	// Reset global tracer provider after test
	original := otel.GetTracerProvider()
	defer otel.SetTracerProvider(original)

	t.Setenv("OTEL_TRACES_EXPORTER", "stdout")
	shutdown, err := InitTracing("test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer shutdown(context.Background())

	tp := otel.GetTracerProvider()
	// Should be a real TracerProvider (not no-op)
	if _, ok := tp.(*noop.TracerProvider); ok {
		t.Fatal("expected real TracerProvider with stdout exporter, got no-op")
	}
}

func TestInitTracing_UnknownExporter(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "unknown-exporter")
	_, err := InitTracing("test-service")
	if err == nil {
		t.Fatal("expected error for unknown exporter, got nil")
	}
}

func TestShutdownTracing(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "")
	shutdown, err := InitTracing("test-service")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Safe to call
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown failed: %v", err)
	}
	// Safe to call again
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("second shutdown failed: %v", err)
	}
}

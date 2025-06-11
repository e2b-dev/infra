package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/encoding/gzip"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type noopSpanExporter struct{}

// ExportSpans handles export of spans by dropping them.
func (nsb *noopSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

// Shutdown stops the exporter by doing nothing.
func (nsb *noopSpanExporter) Shutdown(context.Context) error { return nil }

func NewSpanExporter(ctx context.Context, extraOption ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
	if env.IsLocal() {
		return &noopSpanExporter{}, nil
	}

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(otelCollectorGRPCEndpoint),
		otlptracegrpc.WithCompressor(gzip.Name),
	}
	opts = append(opts, extraOption...)

	// Set up a trace exporter
	traceExporter, traceErr := otlptracegrpc.New(
		ctx,
		opts...,
	)
	if traceErr != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", traceErr)
	}

	return traceExporter, nil
}

func NewTracerProvider(ctx context.Context, spanExporter sdktrace.SpanExporter, serviceName, serviceVersion string, instanceID string) (trace.TracerProvider, error) {
	if env.IsLocal() {
		return noop.NewTracerProvider(), nil
	}

	res, err := getResource(ctx, serviceName, serviceVersion, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(spanExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	return tracerProvider, nil
}

func NewTextPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

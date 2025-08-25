package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/encoding/gzip"
)

type noopSpanExporter struct{}

// ExportSpans handles export of spans by dropping them.
func (nsb *noopSpanExporter) ExportSpans(context.Context, []sdktrace.ReadOnlySpan) error { return nil }

// Shutdown stops the exporter by doing nothing.
func (nsb *noopSpanExporter) Shutdown(context.Context) error { return nil }

func NewSpanExporter(ctx context.Context, extraOption ...otlptracegrpc.Option) (sdktrace.SpanExporter, error) {
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithInsecure(),
		otlptracegrpc.WithEndpoint(OtelCollectorGRPCEndpoint),
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

func NewTracerProvider(ctx context.Context, spanExporter sdktrace.SpanExporter, res *resource.Resource) trace.TracerProvider {
	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(spanExporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)

	return tracerProvider
}

func NewTextPropagator() propagation.TextMapPropagator {
	return propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})
}

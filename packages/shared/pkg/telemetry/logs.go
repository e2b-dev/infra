package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log"
	nooplog "go.opentelemetry.io/otel/log/noop"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	"google.golang.org/grpc/encoding/gzip"
)

// LogProvider extends log.LoggerProvider with a Shutdown method
// so the batch processor and exporter are properly flushed on exit.
type LogProvider interface {
	log.LoggerProvider
	Shutdown(ctx context.Context) error
}

type noopLogProvider struct{ nooplog.LoggerProvider }

func (noopLogProvider) Shutdown(context.Context) error { return nil }

func NewNoopLogProvider() LogProvider { return noopLogProvider{} }

func NewLogProvider(ctx context.Context, res *resource.Resource, extraOpts ...otlploggrpc.Option) (LogProvider, error) {
	opts := []otlploggrpc.Option{
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithEndpoint(otelCollectorGRPCEndpoint),
		otlploggrpc.WithCompressor(gzip.Name),
	}
	opts = append(opts, extraOpts...)

	exporter, err := otlploggrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs exporter: %w", err)
	}

	p := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter)),
	)

	return p, nil
}

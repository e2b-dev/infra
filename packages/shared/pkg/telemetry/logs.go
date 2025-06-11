package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/grpc/encoding/gzip"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type noopLogExporter struct{}

func (noopLogExporter) Export(context.Context, []sdklog.Record) error { return nil }

func (noopLogExporter) Shutdown(context.Context) error { return nil }

func (noopLogExporter) ForceFlush(context.Context) error { return nil }

func NewLogExporter(ctx context.Context, extraOption ...otlploggrpc.Option) (sdklog.Exporter, error) {
	if env.IsLocal() {
		return &noopLogExporter{}, nil
	}

	opts := []otlploggrpc.Option{
		otlploggrpc.WithInsecure(),
		otlploggrpc.WithEndpoint(otelCollectorGRPCEndpoint),
		otlploggrpc.WithCompressor(gzip.Name),
	}
	opts = append(opts, extraOption...)

	logsExporter, err := otlploggrpc.New(
		ctx,
		opts...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create logs exporter: %w", err)
	}

	return logsExporter, nil
}

func NewLogProvider(ctx context.Context, logsExporter sdklog.Exporter, serviceName, serviceVersion, instanceID string) (log.LoggerProvider, error) {
	res, err := getResource(ctx, serviceName, serviceVersion, instanceID)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	logsProcessor := sdklog.NewBatchProcessor(logsExporter)
	logsProvider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(logsProcessor),
	)

	return logsProvider, nil
}

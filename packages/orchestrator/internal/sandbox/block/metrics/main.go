package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Metrics struct {
	SlicesMetric          metric.Int64Histogram
	WriteChunksMetric     metric.Int64Histogram
	ChunkRemoteReadMetric metric.Int64Histogram
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	var m Metrics

	blocksMeter := meterProvider.Meter("internal.sandbox.block.metrics")

	var err error
	if m.SlicesMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.slices",
		metric.WithDescription("Total slices served"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get slices metric: %w", err)
	}

	if m.ChunkRemoteReadMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.chunks.fetch",
		metric.WithDescription("Total chunks fetched"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get fetched chunks metric: %w", err)
	}

	if m.WriteChunksMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.chunks.store",
		metric.WithDescription("Total chunks stored"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get stored chunks metric: %w", err)
	}

	return m, nil
}

func (c Metrics) Begin(metric metric.Int64Histogram) Stopwatch {
	return Stopwatch{metric: metric, start: time.Now()}
}

func KV[T ~string](key string, value T) attribute.KeyValue {
	return attribute.String(key, string(value))
}

type Stopwatch struct {
	metric metric.Int64Histogram
	start  time.Time
}

func (t Stopwatch) End(ctx context.Context, kv ...attribute.KeyValue) {
	amount := time.Since(t.start).Milliseconds()
	t.metric.Record(ctx, amount, metric.WithAttributes(kv...))
}

package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Metrics struct {
	SlicesMetric              metric.Int64Histogram
	WriteChunksMetric         metric.Int64Histogram
	ChunkRemoteReadMetric     metric.Int64Histogram
	TotalBytesFaultedMetric   metric.Int64Counter
	TotalBytesRetrievedMetric metric.Int64Counter
	TotalPageFaults           metric.Int64Counter
	TotalRemoteReadsMetric    metric.Int64Counter
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	var m Metrics

	blocksMeter := meterProvider.Meter("internal.sandbox.block.metrics")

	var err error
	if m.SlicesMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.slices",
		metric.WithDescription("Time taken to retrieve memory slices"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get slices metric: %w", err)
	}

	if m.TotalBytesFaultedMetric, err = blocksMeter.Int64Counter("orchestrator.blocks.slices",
		metric.WithDescription("Total bytes requested"),
		metric.WithUnit("byte"),
	); err != nil {
		return m, fmt.Errorf("failed to create total bytes requested metric: %w", err)
	}

	if m.TotalPageFaults, err = blocksMeter.Int64Counter("orchestrator.blocks.slices",
		metric.WithDescription("Total page faults"),
	); err != nil {
		return m, fmt.Errorf("failed to create total page faults metric: %w", err)
	}

	if m.ChunkRemoteReadMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.chunks.fetch",
		metric.WithDescription("Time taken to retrieve memory chunks from GCP"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get fetched chunks metric: %w", err)
	}

	if m.WriteChunksMetric, err = blocksMeter.Int64Histogram("orchestrator.blocks.chunks.store",
		metric.WithDescription("Time taken to write memory chunks to disk"),
		metric.WithUnit("ms"),
	); err != nil {
		return m, fmt.Errorf("failed to get stored chunks metric: %w", err)
	}

	if m.TotalBytesRetrievedMetric, err = blocksMeter.Int64Counter("orchestrator.blocks.chunks.fetch",
		metric.WithDescription("Total bytes retrieved from remote store"),
		metric.WithUnit("byte"),
	); err != nil {
		return m, fmt.Errorf("failed to create total bytes retrieved from remote store: %w", err)
	}

	if m.TotalRemoteReadsMetric, err = blocksMeter.Int64Counter("orchestrator.blocks.chunks.fetch",
		metric.WithDescription("Total remote fetches"),
	); err != nil {
		return m, fmt.Errorf("failed to create total remote fetches metric: %w", err)
	}

	return m, nil
}

func (c Metrics) Begin(metric metric.Int64Histogram) Stopwatch {
	return Stopwatch{metric: metric, start: time.Now()}
}

func (c Metrics) BeginWithTotal(histogram metric.Int64Histogram, sum, count metric.Int64Counter) StopwatchWithTotal {
	return StopwatchWithTotal{histogram: histogram, sum: sum, count: count, start: time.Now()}
}

type Stopwatch struct {
	metric metric.Int64Histogram
	start  time.Time
}

func (t Stopwatch) End(ctx context.Context, kv ...attribute.KeyValue) {
	amount := time.Since(t.start).Milliseconds()
	t.metric.Record(ctx, amount, metric.WithAttributes(kv...))
}

type StopwatchWithTotal struct {
	histogram  metric.Int64Histogram
	sum, count metric.Int64Counter
	start      time.Time
}

func (t StopwatchWithTotal) End(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	amount := time.Since(t.start).Milliseconds()
	t.histogram.Record(ctx, amount, metric.WithAttributes(kv...))
	t.sum.Add(ctx, total, metric.WithAttributes(kv...))
	t.count.Add(ctx, 1, metric.WithAttributes(kv...))
}

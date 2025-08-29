package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	orchestratorBlockSlices      = "orchestrator.blocks.slices"
	orchestratorBlockChunksFetch = "orchestrator.blocks.chunks.fetch"
	orchestratorBlockChunksStore = "orchestrator.blocks.chunks.store"
)

type TimerFactory struct {
	duration metric.Int64Histogram
	bytes    metric.Int64Counter
	count    metric.Int64Counter
}

func (f *TimerFactory) Begin() *Stopwatch {
	return &Stopwatch{
		histogram: f.duration,
		sum:       f.bytes,
		count:     f.count,
		start:     time.Now(),
	}
}

type Metrics struct {
	// SlicesMetric is used to measure page faulting performance.
	SlicesTimerFactory TimerFactory

	// WriteChunksMetric is used to measure the time taken to download chunks from remote storage
	RemoteReadsTimerFactory TimerFactory

	// WriteChunksMetric is used to measure performance of writing chunks to disk.
	WriteChunksTimerFactory TimerFactory
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	var m Metrics

	blocksMeter := meterProvider.Meter("internal.sandbox.block.metrics")

	var err error
	if m.SlicesTimerFactory, err = createTimerFactory(
		blocksMeter, orchestratorBlockSlices,
		"Time taken to retrieve memory slices",
		"Total bytes requested",
		"Total page faults",
	); err != nil {
		return m, fmt.Errorf("error creating slices timer factory: %v", err)
	}

	if m.RemoteReadsTimerFactory, err = createTimerFactory(
		blocksMeter, orchestratorBlockChunksFetch,
		"Time taken to fetch memory chunks from remote store",
		"Total bytes fetched from remote store",
		"Total remote fetches",
	); err != nil {
		return m, fmt.Errorf("error creating reads timer factory: %v", err)
	}

	if m.WriteChunksTimerFactory, err = createTimerFactory(
		blocksMeter, orchestratorBlockChunksStore,
		"Time taken to write memory chunks to disk",
		"Total bytes written to disk",
		"Total cache writes",
	); err != nil {
		return m, fmt.Errorf("failed to get stored chunks metric: %w", err)
	}

	return m, nil
}

func createTimerFactory(
	blocksMeter metric.Meter,
	metricName, durationDescription, bytesDescription, counterDescription string,
) (TimerFactory, error) {
	duration, err := blocksMeter.Int64Histogram(metricName,
		metric.WithDescription(durationDescription),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to get slices metric: %w", err)
	}

	bytes, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(bytesDescription),
		metric.WithUnit("By"),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total bytes requested metric: %w", err)
	}

	count, err := blocksMeter.Int64Counter(metricName,
		metric.WithDescription(counterDescription),
	)
	if err != nil {
		return TimerFactory{}, fmt.Errorf("failed to create total page faults metric: %w", err)
	}

	return TimerFactory{duration, bytes, count}, nil
}

type Stopwatch struct {
	histogram  metric.Int64Histogram
	sum, count metric.Int64Counter
	start      time.Time
}

func (t Stopwatch) End(ctx context.Context, total int64, kv ...attribute.KeyValue) {
	amount := time.Since(t.start).Milliseconds()
	t.histogram.Record(ctx, amount, metric.WithAttributes(kv...))
	t.sum.Add(ctx, total, metric.WithAttributes(kv...))
	t.count.Add(ctx, 1, metric.WithAttributes(kv...))
}

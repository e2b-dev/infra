package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Metrics struct {
	slices, fetchedChunks metric.Int64Histogram
	storedChunks          metric.Int64Histogram
}

func NewMetrics(meterProvider metric.MeterProvider) (Metrics, error) {
	blocksMeter := meterProvider.Meter("orchestrator.blocks")

	slices, err := blocksMeter.Int64Histogram("slices",
		metric.WithDescription("Total slices served"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to get slices metric: %w", err)
	}

	fetchedChunks, err := blocksMeter.Int64Histogram("chunks.fetch",
		metric.WithDescription("Total chunks fetched"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to get fetched chunks metric: %w", err)
	}

	storedChunks, err := blocksMeter.Int64Histogram("chunks.store",
		metric.WithDescription("Total chunks stored"),
		metric.WithUnit("ms"),
	)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to get stored chunks metric: %w", err)
	}

	return Metrics{
		slices:        slices,
		fetchedChunks: fetchedChunks,
		storedChunks:  storedChunks,
	}, nil
}

func (c Metrics) BeginSlice() Stopwatch {
	return Stopwatch{metric: c.slices, start: time.Now()}
}

func (c Metrics) EndSliceSuccess(ctx context.Context, t Stopwatch, pt pullType) {
	t.success(ctx, attribute.String("pull_type", string(pt)))
}

func (c Metrics) EndSliceFailure(ctx context.Context, t Stopwatch, pt pullType, ft failureType) {
	t.failure(ctx, attribute.String("pull_type", string(pt)), attribute.String("failure_type", string(ft)))
}

func (c Metrics) BeginChunkFetch() Stopwatch {
	return Stopwatch{metric: c.storedChunks, start: time.Now()}
}

func (c Metrics) EndChunkFetchSuccess(ctx context.Context, t Stopwatch) {
	t.success(ctx)
}

func (c Metrics) EndChunkFetchFailure(ctx context.Context, t Stopwatch, ft failureType) {
	t.failure(ctx, attribute.String("failure_type", string(ft)))
}

func (c Metrics) BeginChunkWrite() Stopwatch {
	return Stopwatch{metric: c.storedChunks, start: time.Now()}
}

func (c Metrics) EndChunkWriteSuccess(ctx context.Context, t Stopwatch) {
	t.success(ctx)
}

func (c Metrics) EndChunkWriteFailure(ctx context.Context, t Stopwatch, ft failureType) {
	t.failure(ctx, attribute.String("pull_type", string(ft)))
}

type pullType string

const (
	PullTypeLocal  pullType = "local"
	PullTypeRemote pullType = "remote"
)

type failureType string

const (
	LocalReadFailure  failureType = "local-read"
	ReadAgainFailure  failureType = "read-again"
	RemoteReadFailure failureType = "remote-read"
	LocalWriteFailure failureType = "local-write"
	CacheFetchFailure failureType = "cache-fetch"
)

type Stopwatch struct {
	metric metric.Int64Histogram
	start  time.Time
}

func (t Stopwatch) failure(ctx context.Context, kv ...attribute.KeyValue) {
	kv = append(kv, attribute.String("result", "failure"))
	t.record(ctx, kv...)
}

func (t Stopwatch) success(ctx context.Context, kv ...attribute.KeyValue) {
	kv = append(kv, attribute.String("result", "success"))
	t.metric.Record(ctx, 1, metric.WithAttributes(kv...))
}

func (t Stopwatch) record(ctx context.Context, kv ...attribute.KeyValue) {
	amount := time.Since(t.start).Milliseconds()
	t.metric.Record(ctx, amount, metric.WithAttributes(kv...))
}

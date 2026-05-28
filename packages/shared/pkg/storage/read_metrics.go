package storage

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Instruments for the `orchestrator.read.*` family — one metric per stage
// (open/read/decompress/fetch/writeback) keyed by file_type/source/codec/outcome.

func mustFloatHist(name, desc, unit string) metric.Float64Histogram {
	return utils.Must(meter.Float64Histogram(name,
		metric.WithDescription(desc),
		metric.WithUnit(unit),
	))
}

var (
	readOpen = utils.Must(telemetry.NewFloatTimerFactory(meter,
		"orchestrator.read.open",
		"OpenRangeReader (open / TTFB) wall",
		"Bytes (always 0 — open transfers no payload)",
		"Number of opens",
	))
	readRead = utils.Must(telemetry.NewFloatTimerFactory(meter,
		"orchestrator.read.read",
		"Raw source-read wall (decompression excluded)",
		"Compressed/stored bytes read from the source",
		"Number of source reads",
	))
	readDecompress = utils.Must(telemetry.NewFloatTimerFactory(meter,
		"orchestrator.read.decompress",
		"Decompression CPU wall (decoder read time minus source transfer)",
		"Uncompressed bytes produced",
		"Number of decompress records",
	))
	readFetch = utils.Must(telemetry.NewFloatTimerFactory(meter,
		"orchestrator.read.fetch",
		"Total fetch wall — should ≈ open + read + decompress; any excess is overhead (see read.pipeline.efficiency)",
		"Bytes delivered to the app",
		"Number of fetches",
	))
	readWriteback = utils.Must(telemetry.NewFloatTimerFactory(meter,
		"orchestrator.read.writeback",
		"Async NFS cache writeback wall",
		"Bytes written to NFS",
		"Number of writebacks",
	))

	// fetch / (open + read + decompress); 1.0 = fully explained, >1 = overhead.
	readPipelineEfficiency = mustFloatHist(
		"orchestrator.read.pipeline.efficiency",
		"fetch / (open + read + decompress) — 1.0 = fetch wall fully explained by work, >1 = overhead", "1",
	)

	readCache = utils.Must(meter.Int64Counter(
		"orchestrator.read.cache",
		metric.WithDescription("NFS read-cache events (hit / miss / writeback). The mmap tier is orchestrator.chunk.cache."),
		metric.WithUnit("1"),
	))

	readInflight = utils.Must(meter.Int64UpDownCounter(
		"orchestrator.read.inflight",
		metric.WithDescription("In-flight read-path fetches (cache miss → backend), by file_type"),
		metric.WithUnit("1"),
	))
)

func RecordReadOpen(ctx context.Context, dur time.Duration, bytes int64, attrs metric.MeasurementOption) {
	readOpen.Record(ctx, dur, bytes, attrs)
}

func RecordReadRead(ctx context.Context, dur time.Duration, bytes int64, attrs metric.MeasurementOption) {
	readRead.Record(ctx, dur, bytes, attrs)
}

func RecordReadFetch(ctx context.Context, dur time.Duration, bytes int64, attrs metric.MeasurementOption) {
	readFetch.Record(ctx, dur, bytes, attrs)
}

func RecordReadDecompress(ctx context.Context, dur time.Duration, bytes int64, attrs metric.MeasurementOption) {
	readDecompress.Record(ctx, dur, bytes, attrs)
}

func RecordPipelineEfficiency(ctx context.Context, ratio float64, attrs metric.MeasurementOption) {
	readPipelineEfficiency.Record(ctx, ratio, attrs)
}

// StartInflight increments the read.inflight gauge and returns a func that
// decrements it; defer the returned func so the +1/-1 can't drift apart.
func StartInflight(ctx context.Context, attrs metric.MeasurementOption) func() {
	readInflight.Add(ctx, 1, attrs)

	return func() { readInflight.Add(ctx, -1, attrs) }
}

// Outcome maps a read-path error to the closed read.* outcome enum.
func Outcome(err error) string {
	switch {
	case err == nil:
		return OutcomeOK
	case errors.Is(err, context.Canceled):
		return OutcomeErrCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeErrTimeout
	default:
		return OutcomeErrIO
	}
}

// ErrAttrs builds the error-path attribute set for read.* records. Hot OK
// paths use the precomputed OKAttrs.
func ErrAttrs(o SeekableObjectType, s Source, c CompressionType, err error) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String(AttrFileType, o.String()),
		attribute.String(AttrSource, s.String()),
		attribute.String(AttrCodec, c.String()),
		attribute.String(AttrOutcome, Outcome(err)),
	)
}

func recordDecompressStep(ctx context.Context, r *decompressReader, stats *ReadStats, readErr error) {
	if readErr == nil {
		readDecompress.Record(ctx, stats.Decompress, stats.UncompressedBytes, OKAttrs(r.objType, r.source, r.ct))

		return
	}

	readDecompress.Record(ctx, stats.Decompress, stats.UncompressedBytes, ErrAttrs(r.objType, r.source, r.ct, readErr))
}

// recordWriteback emits the read.writeback timer and its read.cache event.
// src is the originating fetch source (kept for cross-correlation); writebacks
// always target NFS.
func recordWriteback(ctx context.Context, dur time.Duration, bytes int64, ot SeekableObjectType, src Source, ct CompressionType, err error) {
	if err == nil {
		readWriteback.Record(ctx, dur, bytes, OKAttrs(ot, src, ct))
		readCache.Add(ctx, 1, CacheWritebackOKAttrs(ot, SourceNFS, ct))

		return
	}
	readWriteback.Record(ctx, dur, bytes, ErrAttrs(ot, src, ct, err))
	readCache.Add(ctx, 1, CacheWritebackErrAttrs(ot, SourceNFS, ct))
}

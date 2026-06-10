package cleaner

import (
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// Metric attribute keys (Attr*) and the values they take (Val*) used across
// the cleaner.
const (
	AttrDepth    = "depth"
	AttrKind     = "kind"
	AttrSource   = "source"
	AttrResult   = "result"
	AttrAction   = "action"
	AttrLayout   = "layout"
	AttrScenario = "scenario"
	AttrScale    = "scale"

	ValKindFile               = "file"
	ValKindDir                = "dir"
	ValKindTrulyEmpty         = "truly_empty"
	ValKindOrphanNoMemRootfs  = "orphan_no_memfile_rootfs"
	ValSrcInDir               = "in_dir"
	ValSrcAlone               = "standalone"
	ValResultOk               = "ok"
	ValResultErr              = "err"
	ValResultAGN              = "already_gone"
	ValResultSAC              = "skipped_atime_changed"
	ValActionDeleted          = "deleted"
	ValActionSkippedGrace     = "skipped_grace_period"
	ValActionDeleteFailed     = "delete_failed"

	ValLayoutFlat    = "flat"
	ValLayoutSharded = "sharded"
)

// Metrics holds the OTEL instruments emitted by the cleaner. Construct via
// NewMetrics; for no-op behaviour pass a noop.MeterProvider().Meter("…").
type Metrics struct {
	// Per-directory scan timings & shape.
	ScanReaddirDuration   metric.Int64Histogram // ms; attrs: depth
	ScanStatPhaseDuration metric.Int64Histogram // ms; attrs: depth
	ScanEntries           metric.Int64Histogram // count; attrs: depth, kind

	// Per-stat & per-delete timings.
	StatDuration         metric.Int64Histogram // ms
	BatchAssembleDuration metric.Int64Histogram // ms; wall time per BatchN candidates
	DeleteQueueWait      metric.Int64Histogram // ms; enqueue → Deleter pickup
	DeleteUnlinkDuration metric.Int64Histogram // ms; os.Remove wall time

	// Op counters.
	ReaddirOps metric.Int64Counter // attrs: depth
	StatOps    metric.Int64Counter // attrs: source
	UnlinkOps  metric.Int64Counter // attrs: result
	RmdirOps   metric.Int64Counter // attrs: result
	ScanBusy   metric.Int64Counter // ErrBusy retries on scanDir

	// Empty/orphan directory encounters during scan.
	EmptyDirEncountered metric.Int64Counter // attrs: kind, action

	// Sharding A/B benchmark instruments. Recorded only by the
	// --bench-shard-read mode of the cleaner binary.
	BenchReadDuration metric.Int64Histogram // us; attrs: layout, scenario
	BenchReadOps      metric.Int64Counter   // attrs: layout, scenario, result
}

// NewMetrics builds all instruments from the given meter. A noop meter
// produces noop instruments that are safe to call but record nothing.
func NewMetrics(meter metric.Meter) (*Metrics, error) {
	m := &Metrics{}
	var err error

	if m.ScanReaddirDuration, err = meter.Int64Histogram(
		"clean.nfs.scan.readdir.duration",
		metric.WithDescription("Duration of the READDIR phase of a directory scan"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("scan.readdir.duration: %w", err)
	}

	if m.ScanStatPhaseDuration, err = meter.Int64Histogram(
		"clean.nfs.scan.stat_phase.duration",
		metric.WithDescription("Duration of the stat phase of a directory scan (submit + drain all per-entry stats)"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("scan.stat_phase.duration: %w", err)
	}

	if m.ScanEntries, err = meter.Int64Histogram(
		"clean.nfs.scan.entries",
		metric.WithDescription("Number of entries observed in a directory by a scan, split by kind"),
		metric.WithUnit("{entry}"),
	); err != nil {
		return nil, fmt.Errorf("scan.entries: %w", err)
	}

	if m.StatDuration, err = meter.Int64Histogram(
		"clean.nfs.stat.duration",
		metric.WithDescription("Duration of a single statx call (in-dir or standalone)"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("stat.duration: %w", err)
	}

	if m.BatchAssembleDuration, err = meter.Int64Histogram(
		"clean.nfs.batch.assemble.duration",
		metric.WithDescription("Wall time to assemble a full batch of BatchN candidates"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("batch.assemble.duration: %w", err)
	}

	if m.DeleteQueueWait, err = meter.Int64Histogram(
		"clean.nfs.delete.queue_wait.duration",
		metric.WithDescription("Time a candidate spent waiting in the delete channel before a Deleter picked it up"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("delete.queue_wait.duration: %w", err)
	}

	if m.DeleteUnlinkDuration, err = meter.Int64Histogram(
		"clean.nfs.delete.unlink.duration",
		metric.WithDescription("Wall time of a single os.Remove call"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("delete.unlink.duration: %w", err)
	}

	if m.ReaddirOps, err = meter.Int64Counter(
		"clean.nfs.readdir.ops",
		metric.WithDescription("Total ReadDir(2048) syscalls issued during scans"),
	); err != nil {
		return nil, fmt.Errorf("readdir.ops: %w", err)
	}

	if m.StatOps, err = meter.Int64Counter(
		"clean.nfs.stat.ops",
		metric.WithDescription("Total statx calls (in-dir during scan, or standalone before delete)"),
	); err != nil {
		return nil, fmt.Errorf("stat.ops: %w", err)
	}

	if m.UnlinkOps, err = meter.Int64Counter(
		"clean.nfs.unlink.ops",
		metric.WithDescription("Total file delete attempts, by outcome"),
	); err != nil {
		return nil, fmt.Errorf("unlink.ops: %w", err)
	}

	if m.RmdirOps, err = meter.Int64Counter(
		"clean.nfs.rmdir.ops",
		metric.WithDescription("Total empty-directory removals, by outcome"),
	); err != nil {
		return nil, fmt.Errorf("rmdir.ops: %w", err)
	}

	if m.ScanBusy, err = meter.Int64Counter(
		"clean.nfs.scan.busy",
		metric.WithDescription("ErrBusy hits when a scanner tried a directory another scanner had already claimed"),
	); err != nil {
		return nil, fmt.Errorf("scan.busy: %w", err)
	}

	if m.EmptyDirEncountered, err = meter.Int64Counter(
		"clean.nfs.empty_dir.encountered",
		metric.WithDescription("Directories scanned that were either truly empty or orphaned (no memfile/ or rootfs.ext4/ subdir), split by what we did about it"),
	); err != nil {
		return nil, fmt.Errorf("empty_dir.encountered: %w", err)
	}

	if m.BenchReadDuration, err = meter.Int64Histogram(
		"clean.nfs.bench.read.duration",
		metric.WithDescription("Wall time of a single os.ReadFile (LOOKUP+OPEN+READ+CLOSE) in the sharding A/B benchmark"),
		metric.WithUnit("us"),
	); err != nil {
		return nil, fmt.Errorf("bench.read.duration: %w", err)
	}

	if m.BenchReadOps, err = meter.Int64Counter(
		"clean.nfs.bench.read.ops",
		metric.WithDescription("Total ReadFile attempts performed in the sharding A/B benchmark, by outcome"),
	); err != nil {
		return nil, fmt.Errorf("bench.read.ops: %w", err)
	}

	return m, nil
}

// NoopMetrics returns a Metrics whose instruments record nothing. Useful for
// tests and for runs without an OTEL collector endpoint.
func NoopMetrics() *Metrics {
	m, err := NewMetrics(noop.NewMeterProvider().Meter("clean-nfs-cache-noop"))
	if err != nil {
		// noop meter cannot return errors; this is unreachable.
		panic(fmt.Sprintf("noop metrics construction failed: %v", err))
	}
	return m
}

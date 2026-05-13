package sandbox

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	snapshotDiffBytes    = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffBytes))
	snapshotDiffRatioPct = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffRatioPct))
	snapshotTotalBytes   = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotTotalBytes))
)

type SnapshotUseCase string

const (
	SnapshotUseCasePause SnapshotUseCase = "pause"
	SnapshotUseCaseBuild SnapshotUseCase = "build"
)

func RecordSnapshotDiffMetrics(ctx context.Context, snap *Snapshot, useCase SnapshotUseCase) {
	if snap == nil {
		return
	}
	uc := attribute.String("use_case", string(useCase))
	emitSnapshotDiff(ctx, "memfile", snap.MemfileDiffStats, uc)
	emitSnapshotDiff(ctx, "rootfs", snap.RootfsDiffStats, uc)
}

func emitSnapshotDiff(ctx context.Context, fileType string, s SnapshotDiffStats, uc attribute.KeyValue) {
	ft := attribute.String("file_type", fileType)
	snapshotTotalBytes.Record(ctx, s.TotalBytes, metric.WithAttributes(ft, uc))
	// "full" = blocks that carry data in this snapshot (DiffMetadata.Dirty);
	// "empty" = zero/Empty-mapped blocks (DiffMetadata.Empty).
	for kind, b := range map[string]int64{"full": s.DirtyBytes, "empty": s.EmptyBytes} {
		attrs := metric.WithAttributes(ft, attribute.String("kind", kind), uc)
		snapshotDiffBytes.Record(ctx, b, attrs)
		snapshotDiffRatioPct.Record(ctx, ratioPct(b, s.TotalBytes), attrs)
	}
}

func ratioPct(num, denom int64) int64 {
	if denom <= 0 {
		return 0
	}
	pct := num * 100 / denom
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}

	return pct
}

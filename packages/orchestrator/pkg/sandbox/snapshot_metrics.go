package sandbox

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	snapshotDiffBytes   = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffBytes))
	snapshotDiffRatioBp = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffRatioBp))
	snapshotTotalBytes  = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotTotalBytes))
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
	for kind, b := range map[string]int64{"dirty": s.DirtyBytes, "empty": s.EmptyBytes} {
		attrs := metric.WithAttributes(ft, attribute.String("kind", kind), uc)
		snapshotDiffBytes.Record(ctx, b, attrs)
		snapshotDiffRatioBp.Record(ctx, ratioBp(b, s.TotalBytes), attrs)
	}
}

func ratioBp(num, denom int64) int64 {
	if denom <= 0 {
		return 0
	}
	bp := num * 10000 / denom
	if bp < 0 {
		return 0
	}
	if bp > 10000 {
		return 10000
	}

	return bp
}

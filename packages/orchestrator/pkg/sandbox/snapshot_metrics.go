package sandbox

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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

func recordSnapshotDiff(
	ctx context.Context,
	fileType string,
	dm *header.DiffMetadata,
	original *header.Header,
) {
	if dm == nil || original == nil || original.Metadata == nil {
		return
	}
	bs := int64(original.Metadata.BlockSize)
	total := int64(original.Metadata.Size)

	ft := attribute.String("file_type", fileType)

	snapshotTotalBytes.Record(ctx, total, metric.WithAttributes(ft))

	var dirtyBytes, emptyBytes int64
	if dm.Dirty != nil {
		dirtyBytes = int64(dm.Dirty.GetCardinality()) * bs
	}
	if dm.Empty != nil {
		emptyBytes = int64(dm.Empty.GetCardinality()) * bs
	}
	for kind, b := range map[string]int64{
		"dirty": dirtyBytes,
		"empty": emptyBytes,
	} {
		attrs := metric.WithAttributes(ft, attribute.String("kind", kind))
		snapshotDiffBytes.Record(ctx, b, attrs)
		snapshotDiffRatioBp.Record(ctx, ratioBp(b, total), attrs)
	}
}

// ratioBp returns num/denom in basis points (10000 = 100.00%) so we keep
// sub-percent resolution. Grafana panels divide by 100 to display percent.
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

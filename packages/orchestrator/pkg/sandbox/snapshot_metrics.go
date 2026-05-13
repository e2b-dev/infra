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
	snapshotDiffBytes    = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffBytes))
	snapshotDiffRatioPct = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffRatioPct))
	snapshotTotalBytes   = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotTotalBytes))
)

type SnapshotUseCase string

const (
	SnapshotUseCasePause SnapshotUseCase = "pause"
	SnapshotUseCaseBuild SnapshotUseCase = "build"
)

// recordSnapshotDiff emits per-snapshot full/empty/total bytes for one file.
// Call right after the per-pause DiffMetadata for that file is produced.
// "full" = blocks carrying data (DiffMetadata.Dirty); "empty" = zero-mapped
// blocks (DiffMetadata.Empty). Total comes from the original (pre-merge)
// header so the denominator is the file's full mapped size.
func recordSnapshotDiff(
	ctx context.Context,
	fileType string,
	dm *header.DiffMetadata,
	original *header.Header,
	useCase SnapshotUseCase,
) {
	if dm == nil || original == nil || original.Metadata == nil {
		return
	}
	bs := int64(original.Metadata.BlockSize)
	total := int64(original.Metadata.Size)

	ft := attribute.String("file_type", fileType)
	uc := attribute.String("use_case", string(useCase))

	snapshotTotalBytes.Record(ctx, total, metric.WithAttributes(ft, uc))

	var fullBytes, emptyBytes int64
	if dm.Dirty != nil {
		fullBytes = int64(dm.Dirty.GetCardinality()) * bs
	}
	if dm.Empty != nil {
		emptyBytes = int64(dm.Empty.GetCardinality()) * bs
	}
	for kind, b := range map[string]int64{
		"full":  fullBytes,
		"empty": emptyBytes,
	} {
		attrs := metric.WithAttributes(ft, attribute.String("kind", kind), uc)
		snapshotDiffBytes.Record(ctx, b, attrs)
		snapshotDiffRatioPct.Record(ctx, ratioPct(b, total), attrs)
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

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
	snapshotDiffBytes  = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotDiffBytes))
	snapshotDiffRatio  = utils.Must(telemetry.GetFloatHistogram(meter, telemetry.SnapshotDiffRatio))
	snapshotTotalBytes = utils.Must(telemetry.GetHistogram(meter, telemetry.SnapshotTotalBytes))
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
		snapshotDiffRatio.Record(ctx, ratioFraction(b, total), attrs)
	}
}

// ratioFraction returns num/denom as a fraction in [0,1] (1.0 = 100%). Emitted
// with unit {1} so Grafana percent formatting renders it directly.
func ratioFraction(num, denom int64) float64 {
	if denom <= 0 {
		return 0
	}
	r := float64(num) / float64(denom)
	if r < 0 {
		return 0
	}
	if r > 1 {
		return 1
	}

	return r
}

//go:build linux

package build

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// readSegmentsMetric and readBuildsMetric record, once per successful
// File.ReadAt, how many build-backed segments the read decomposed into and how
// many distinct builds those segments referenced. They are the direct
// resume-side measure of snapshot fragmentation: page-granular dedup
// interleaves a header's mapping across many ancestor builds, and a single
// hugepage fault then fans out into one backing read per mapping run it
// crosses (serially, unless MaxParallelBuildReadSegments raises the limit).
// Zero-filled (uuid.Nil) runs cost no I/O and are not counted.
var (
	readSegmentsMetric = utils.Must(meter.Int64Histogram("orchestrator.build.read.segments",
		metric.WithDescription("Build-backed segments a single build.File read decomposed into")))
	readBuildsMetric = utils.Must(meter.Int64Histogram("orchestrator.build.read.builds",
		metric.WithDescription("Distinct builds referenced by a single build.File read")))
)

// readFanoutAttrs precomputes the per-file-type attribute sets so the
// per-read hot path allocates nothing (mirrors uffd/userfaultfd/metrics.go).
var readFanoutAttrs = map[DiffType]metric.MeasurementOption{
	Memfile: telemetry.PrecomputeAttrs(attribute.String("file_type", string(Memfile))),
	Rootfs:  telemetry.PrecomputeAttrs(attribute.String("file_type", string(Rootfs))),
}

// recordReadFanout records the fan-out of one completed File.ReadAt.
func recordReadFanout(ctx context.Context, fileType DiffType, segments, builds int) {
	attrs, ok := readFanoutAttrs[fileType]
	if !ok {
		attrs = telemetry.PrecomputeAttrs(attribute.String("file_type", string(fileType)))
	}
	readSegmentsMetric.Record(ctx, int64(segments), attrs)
	readBuildsMetric.Record(ctx, int64(builds), attrs)
}

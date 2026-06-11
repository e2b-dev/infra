//go:build linux

package template

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Header-shape metrics, recorded once per header load (template-cache miss on
// a node). Together they describe how fragmented the snapshots being resumed
// are — the static upper bound on the per-read fan-out that
// orchestrator.build.read.* measures at fault time:
//
//   - generation: pause/resume cycles behind this snapshot
//   - mapping_entries: runs in the flattened mapping
//   - distinct_builds: ancestor builds still referenced (each is a separate
//     storage object + local cache file + chunker at resume)
//   - max_entries_per_block: worst-case build-backed runs intersecting one
//     2 MiB block — the ceiling on segments a single hugepage fault fans
//     out into
var (
	headerGenerationMetric = utils.Must(meter.Int64Histogram("orchestrator.header.generation",
		metric.WithDescription("Snapshot generation (pause/resume cycles) of a loaded header")))
	headerMappingEntriesMetric = utils.Must(meter.Int64Histogram("orchestrator.header.mapping_entries",
		metric.WithDescription("Mapping entries (runs) in a loaded header")))
	headerDistinctBuildsMetric = utils.Must(meter.Int64Histogram("orchestrator.header.distinct_builds",
		metric.WithDescription("Distinct builds referenced by a loaded header's mapping")))
	headerMaxEntriesPerBlockMetric = utils.Must(meter.Int64Histogram("orchestrator.header.max_entries_per_block",
		metric.WithDescription("Max build-backed mapping runs intersecting one 2 MiB block of a loaded header")))
)

var headerShapeAttrs = map[build.DiffType]metric.MeasurementOption{
	build.Memfile: telemetry.PrecomputeAttrs(attribute.String("file_type", string(build.Memfile))),
	build.Rootfs:  telemetry.PrecomputeAttrs(attribute.String("file_type", string(build.Rootfs))),
}

// recordHeaderShape records the fragmentation shape of a freshly loaded
// header. Cheap relative to the load itself: one O(entries) scan.
func recordHeaderShape(ctx context.Context, fileType build.DiffType, h *header.Header) {
	if h == nil || h.Metadata == nil {
		return
	}

	attrs, ok := headerShapeAttrs[fileType]
	if !ok {
		attrs = telemetry.PrecomputeAttrs(attribute.String("file_type", string(fileType)))
	}

	headerGenerationMetric.Record(ctx, int64(h.Metadata.Generation), attrs)
	headerMappingEntriesMetric.Record(ctx, int64(h.Mapping.Len()), attrs)
	headerDistinctBuildsMetric.Record(ctx, int64(len(h.Mapping.Builds())), attrs)
	headerMaxEntriesPerBlockMetric.Record(ctx, maxEntriesPerBlock(h.Mapping), attrs)
}

// maxEntriesPerBlock returns the maximum number of build-backed mapping runs
// intersecting any single 2 MiB-aligned block of the virtual space. Empty
// (uuid.Nil) runs are skipped: they zero-fill without I/O, so they don't add
// to a fault's fan-out. Relies on mapping runs being sorted and
// non-overlapping (validated at header construction).
func maxEntriesPerBlock(m header.Mapping) int64 {
	var maxCount, count int64
	curBlock := int64(-1)

	for _, bm := range m.All() {
		if bm.BuildId == uuid.Nil || bm.Length == 0 {
			continue
		}

		start := int64(bm.Offset) / header.HugepageSize
		end := int64(bm.Offset+bm.Length-1) / header.HugepageSize

		if start == curBlock {
			count++
		} else {
			count = 1
		}
		maxCount = max(maxCount, count)

		// A run spilling past its first block is the only one intersecting the
		// later blocks so far, so the running count there restarts at 1.
		curBlock = end
		if end > start {
			count = 1
		}
	}

	return maxCount
}

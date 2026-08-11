//go:build linux

package uffd

import (
	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var uffdMeter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd")

// dirtyDivergenceMetricName records, per pause of a sync-WP sandbox, how the
// page tracker's dirty view compares with the pagemap readout — the burn-in
// evidence for serving the dirty set from the tracker (and retiring the
// pagemap read). One histogram sample per pause per set, value = page count:
//
//	set="tracker_only":  tracker Dirty, pagemap clean. Expected for
//	                     prefault-installed pages that were never written.
//	set="pagemap_only":  pagemap dirty, tracker unaware. The set that must
//	                     hold at zero for the tracker to be a safe source —
//	                     any sustained non-zero reading here blocks the
//	                     sync-wp-tracker-dirty rollout.
//	set="pagemap_dirty": the pagemap's full dirty count, as the denominator
//	                     for reading the two divergence sets in proportion.
//
// Emitted only when the handler resolved at least one sync WP fault this run
// (same gate as the divergence log): under WP_ASYNC the pagemap diverges from
// the tracker on every pause by design, and recording that fleet-wide would
// drown the signal this metric exists for.
const dirtyDivergenceMetricName = "orchestrator.sandbox.uffd.dirty_divergence"

var dirtyDivergencePages = utils.Must(uffdMeter.Int64Histogram(dirtyDivergenceMetricName,
	metric.WithDescription("Per-pause dirty-set divergence between the page tracker and the pagemap readout on sync-WP sandboxes, in pages"),
	metric.WithUnit("{page}"),
))

var dirtyDivergenceAttrs = map[string]metric.MeasurementOption{
	"tracker_only":  telemetry.PrecomputeAttrs(attribute.String("set", "tracker_only")),
	"pagemap_only":  telemetry.PrecomputeAttrs(attribute.String("set", "pagemap_only")),
	"pagemap_dirty": telemetry.PrecomputeAttrs(attribute.String("set", "pagemap_dirty")),
}

// divergenceCardinalities computes the dirty-set divergence counts between
// the tracker's view and the pagemap readout: pages only the tracker holds
// dirty, pages only the pagemap holds dirty, and the pagemap's total (the
// denominator). Cardinalities only — one AndCardinality instead of
// materializing two AndNot bitmaps on the latency-sensitive pause path.
func divergenceCardinalities(tracker, pagemap *roaring.Bitmap) (trackerOnly, pagemapOnly, pagemapDirty uint64) {
	both := tracker.AndCardinality(pagemap)

	return tracker.GetCardinality() - both, pagemap.GetCardinality() - both, pagemap.GetCardinality()
}

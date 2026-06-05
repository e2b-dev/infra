//go:build linux

package prefetch

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/prefetch")

// pagesCounter counts pages processed by prefetch runs, by stage:
// fetched / fetch_skipped (fetch phase: cache population from the source) and
// copied / copy_skipped (copy phase: Prefault into the guest). copied counts
// only pages this run actually installed — equal to
// prefault{result="installed"} by construction; prefaults that found the page
// already resident, lost the install race, or hit EAGAIN land in
// copy_skipped. Recorded once per run at completion, matching the
// "prefetch: completed" log line. Per-page copy detail (latency, install
// outcome) lives in orchestrator.sandbox.uffd.prefault.
var pagesCounter = utils.Must(meter.Int64Counter(
	"orchestrator.sandbox.uffd.prefetch.pages",
	metric.WithDescription("Pages processed by prefetch runs, by stage"),
))

// durationHistogram records prefetch run phase durations. The fetch and copy
// phases overlap (copy starts once uffd is ready); "total" spans the whole
// run. copy/total durations far exceeding the resume duration mean the
// prefetch ran too late to help.
var durationHistogram = utils.Must(meter.Int64Histogram(
	"orchestrator.sandbox.uffd.prefetch.duration",
	metric.WithDescription("Prefetch run phase durations"),
	metric.WithUnit("ms"),
))

var (
	stageFetchedAttr      = telemetry.PrecomputeAttrs(attribute.String("stage", "fetched"))
	stageFetchSkippedAttr = telemetry.PrecomputeAttrs(attribute.String("stage", "fetch_skipped"))
	stageCopiedAttr       = telemetry.PrecomputeAttrs(attribute.String("stage", "copied"))
	stageCopySkippedAttr  = telemetry.PrecomputeAttrs(attribute.String("stage", "copy_skipped"))

	phaseFetchAttr = telemetry.PrecomputeAttrs(attribute.String("phase", "fetch"))
	phaseCopyAttr  = telemetry.PrecomputeAttrs(attribute.String("phase", "copy"))
	phaseTotalAttr = telemetry.PrecomputeAttrs(attribute.String("phase", "total"))
)

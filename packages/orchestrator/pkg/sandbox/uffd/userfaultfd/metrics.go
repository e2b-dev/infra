//go:build linux

package userfaultfd

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/uffd/userfaultfd")

// serveMetricName is the metric under which per-fault serve latency / faulted
// bytes / fault count are reported (a TimerFactory triplet).
const serveMetricName = "orchestrator.sandbox.uffd.serve"

// serveTimer records, per served UFFD demand fault, the serve latency (ms
// histogram), bytes installed into the guest by this serve (counter) and the
// serve-attempt count (counter) under serveMetricName. It is always on: the
// fault-in window right after a resume is the prime suspect for slow resumes,
// and this is the only fleet-wide signal for it.
//
// Scope: demand faults only. Pages installed by the prefetcher (Prefault)
// bypass the serve loop and are not counted here; a demand fault that loses
// the install race to a concurrent worker or prefault is recorded as
// result="present" with zero bytes, so the bytes counter only ever counts
// pages this serve actually copied.
var serveTimer = utils.Must(telemetry.NewTimerFactory(
	meter,
	serveMetricName,
	"Time to serve a UFFD demand page fault",
	"Bytes installed into the guest by demand-fault serves",
	"UFFD demand-fault serve attempts",
))

// pageClass classifies a fault by the page-tracker state at the time it was
// served — the distinction that matters for diagnosing slow resumes, since only
// pageClassNew faults reach the source (and may hit GCS).
type pageClass uint8

const (
	pageClassNew      pageClass = iota // block.NotPresent: pulled from the source chunker
	pageClassZero                      // block.Zero: zero-filled
	pageClassResident                  // block.Dirty: already present, short-circuited
	pageClassUnknown                   // classification failed (unexpected tracker state)
	numPageClass
)

// faultResult is the terminal outcome of serving a fault.
type faultResult uint8

const (
	faultResultInstalled faultResult = iota // page installed by this serve
	faultResultPresent                      // page already present: resident short-circuit or lost install race (EEXIST)
	faultResultDeferred                     // EAGAIN: must be retried later
	faultResultDiscarded                    // ESRCH: faulting thread gone, retry pointless
	faultResultError                        // serving failed
	faultResultSkipped                      // prefault only: tracker already Dirty/Zero — prefetch arrived too late
	numFaultResult
)

// resultNames maps faultResult values to their metric label strings.
var resultNames = [numFaultResult]string{
	faultResultInstalled: "installed",
	faultResultPresent:   "present",
	faultResultDeferred:  "deferred",
	faultResultDiscarded: "discarded",
	faultResultError:     "error",
	faultResultSkipped:   "skipped",
}

// serveAttrs holds a precomputed metric.MeasurementOption per
// (pageClass, faultResult) combination so the per-fault hot path allocates no
// attributes (mirrors the precomputed attrs in block/streaming_chunk.go).
var serveAttrs = buildServeAttrs()

func buildServeAttrs() [numPageClass][numFaultResult]metric.MeasurementOption {
	classNames := [numPageClass]string{
		pageClassNew:      "new",
		pageClassZero:     "zero",
		pageClassResident: "resident",
		pageClassUnknown:  "unknown",
	}

	var t [numPageClass][numFaultResult]metric.MeasurementOption
	for c := range classNames {
		for r := range resultNames {
			t[c][r] = telemetry.PrecomputeAttrs(
				attribute.String("page_class", classNames[c]),
				attribute.String("result", resultNames[r]),
			)
		}
	}

	return t
}

// prefaultMetricName is the metric under which per-page prefault latency /
// installed bytes / attempt count are reported (a TimerFactory triplet).
const prefaultMetricName = "orchestrator.sandbox.uffd.prefault"

// prefaultTimer records, per Prefault call, the install latency (ms
// histogram), bytes installed into the guest by this prefault (counter) and
// the attempt count (counter) under prefaultMetricName. Prefault data is
// already in memory, so the latency is lock wait + UFFDIO_COPY — a host
// contention proxy rather than a storage signal. Together with serveTimer it
// makes memory materialization race-proof: whichever side loses the EEXIST
// install race records result="present" with zero bytes, so
// serve.bytes + prefault.bytes never double-counts a page.
var prefaultTimer = utils.Must(telemetry.NewTimerFactory(
	meter,
	prefaultMetricName,
	"Time to prefault a page into the guest",
	"Bytes installed into the guest by prefaults",
	"UFFD prefault attempts",
))

// prefaultAttrs holds a precomputed metric.MeasurementOption per faultResult.
// Prefaults have no page_class: they only ever target not-present pages (the
// Dirty/Zero pre-check records result="skipped" instead).
var prefaultAttrs = buildPrefaultAttrs()

func buildPrefaultAttrs() [numFaultResult]metric.MeasurementOption {
	var t [numFaultResult]metric.MeasurementOption
	for r := range resultNames {
		t[r] = telemetry.PrecomputeAttrs(attribute.String("result", resultNames[r]))
	}

	return t
}

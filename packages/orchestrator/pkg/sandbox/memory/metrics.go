//go:build linux

package memory

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory")

var (
	// sharedPageHits tracks how many times a read hit a shared physical page.
	sharedPageHits = utils.Must(telemetry.GetCounter(meter, "shared_memfile.page_hits"))

	// cowFaultsTotal tracks CoW fault count when a VM writes to a shared page.
	cowFaultsTotal = utils.Must(telemetry.GetCounter(meter, "shared_memfile.cow_faults"))

	// sharedMemoryBytes is the total bytes of shared memfile mappings.
	sharedMemoryBytes = utils.Must(telemetry.GetCounter(meter, "shared_memfile.bytes"))

	// privateMemoryBytes estimates private (CoW-ed) memory bytes.
	privateMemoryBytes = utils.Must(telemetry.GetCounter(meter, "shared_memfile.private_bytes"))

	// sharedMemfileMapCount is the number of active shared memfile mappings.
	sharedMemfileMapCount = utils.Must(telemetry.GetCounter(meter, "shared_memfile.map_count"))

	// sharedMemfileMapDuration records mmap operation latency in milliseconds.
	sharedMemfileMapDuration = utils.Must(telemetry.GetHistogram(meter, "shared_memfile.map_duration_ms"))

	// cowPagesReclaimed tracks CoW pages reclaimed via MADV_DONTNEED.
	cowPagesReclaimed = utils.Must(telemetry.GetCounter(meter, "shared_memfile.cow_pages_reclaimed"))

	// cowPagesSaved tracks CoW pages saved to L2 checkpoint.
	cowPagesSaved = utils.Must(telemetry.GetCounter(meter, "shared_memfile.cow_pages_saved"))

	// sharedPagesCold tracks bytes de-prioritized via MADV_COLD.
	sharedPagesCold = utils.Must(telemetry.GetCounter(meter, "shared_memfile.cold_bytes"))

	// pteBatchPrefaultPages tracks pages pre-faulted via MADV_POPULATE_WRITE.
	pteBatchPrefaultPages = utils.Must(telemetry.GetCounter(meter, "shared_memfile.pte_batch_prefault_pages"))

	// l2CheckpointLoadDuration records L2 checkpoint load latency in ms.
	l2CheckpointLoadDuration = utils.Must(telemetry.GetHistogram(meter, "shared_memfile.l2_checkpoint_load_ms"))

	// thpPagesAdvised tracks pages advised with MADV_HUGEPAGE for THP optimization.
	thpPagesAdvised = utils.Must(telemetry.GetCounter(meter, "shared_memfile.thp_pages_advised"))

	// thpRegionsAdvised tracks the number of memory regions advised for THP.
	thpRegionsAdvised = utils.Must(telemetry.GetCounter(meter, "shared_memfile.thp_regions_advised"))
)

// RecordCowFault records a CoW page fault attributed to a specific layer.
func RecordCowFault(layer string) {
	cowFaultsTotal.Add(nil, 1, metric.WithAttributes(attribute.String("layer", layer)))
}

// RecordSharedPageHit records a read that hit a shared physical page.
func RecordSharedPageHit() {
	sharedPageHits.Add(nil, 1)
}

// RecordPrivateBytesGrowth records additional private (CoW-ed) bytes.
func RecordPrivateBytesGrowth(bytes int64) {
	privateMemoryBytes.Add(nil, bytes)
}

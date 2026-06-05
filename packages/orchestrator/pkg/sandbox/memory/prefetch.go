//go:build linux

package memory

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var prefetchTracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory/prefetch")

// PrefetchPriority assigns an urgency tier to each memory region.
// Lower numbers = higher priority.
type PrefetchPriority int

const (
	// P0 — Gateway listening port ready (microseconds).
	// Pages needed for envd to accept the first health check.
	PriorityP0 PrefetchPriority = iota

	// P1 — First request processing (milliseconds).
	// Pages needed to process the first user message.
	PriorityP1

	// P2 — Full functionality (tens of milliseconds).
	// Remaining hot code paths and data.
	PriorityP2

	// P3 — Cold data (lazy/on-demand).
	// Infrequently accessed pages loaded only when demanded.
	PriorityP3
)

// PrefetchRegion describes a contiguous memory region to prefetch
// with its assigned priority.
type PrefetchRegion struct {
	Start    int64 // offset into memfile, bytes
	Size     int64 // region size, bytes
	Priority PrefetchPriority
}

// FileBackendPrefetcher pre-faults pages into MAP_PRIVATE mmap regions
// before the Firecracker VM accesses them. It works with the File memory
// backend (template snapshot resume), not UFFD.
//
// Design follows the FaaSnap approach:
//   - Pages are grouped in 1024-page batches (4MB at 4KB pages)
//   - Within a group, pages are fetched sequentially (host readahead)
//   - Groups are dispatched concurrently to worker goroutines
//   - Higher-priority regions (P0→P3) are processed first
//
// The prefetcher touches each page of the mmap'd region to force
// the kernel to establish PTEs. For shared (MAP_PRIVATE) memfiles,
// read-only pages hit the host page cache; CoW pages are allocated
// only on write.
type FileBackendPrefetcher struct {
	logger logger.Logger
}

// NewFileBackendPrefetcher creates a file-backend page prefetcher.
func NewFileBackendPrefetcher(logger logger.Logger) *FileBackendPrefetcher {
	return &FileBackendPrefetcher{logger: logger}
}

// PrefetchConfig controls parallelism and grouping behavior.
type PrefetchConfig struct {
	// GroupSize is the number of pages per batch. Default: 1024 (4MB at 4KB).
	GroupSize int

	// MaxConcurrent is the maximum number of concurrent worker goroutines.
	// Default: 8.
	MaxConcurrent int

	// PageSize is the page size in bytes. Default: 4096 (Linux).
	PageSize int64
}

// DefaultPrefetchConfig returns a prefetch config with sensible defaults.
func DefaultPrefetchConfig() PrefetchConfig {
	pageSize := int64(unix.Getpagesize())
	return PrefetchConfig{
		GroupSize:     1024,
		MaxConcurrent: 8,
		PageSize:      pageSize,
	}
}

// PrefetchStats holds counters collected during a prefetch run.
type PrefetchStats struct {
	TotalPages       uint64
	FaultedPages     atomic.Uint64
	SkippedPages     atomic.Uint64
	CompletedBatches atomic.Uint64
}

// workItem represents a chunk of work for prefetch workers.
type workItem struct {
	start    int64 // byte offset into memfile
	end      int64 // byte offset (exclusive)
	priority PrefetchPriority
}

// Prefetch pre-faults pages in the given regions of a shared memfile.
// Pages are grouped into batches and dispatched to concurrent workers.
// Regions are processed in priority order (P0 first). The call blocks
// until all pages are touched or ctx is cancelled.
//
// The data parameter is the mmap'd region from SharedMemfileManager.
// We touch each page to cause the kernel to establish PTEs (minor fault
// for shared pages, CoW-fault for private pages).
func (p *FileBackendPrefetcher) Prefetch(
	ctx context.Context,
	data []byte,
	dataSize int64,
	regions []PrefetchRegion,
	config PrefetchConfig,
) *PrefetchStats {
	ctx, span := prefetchTracer.Start(ctx, "file-backend-prefetch")
	defer span.End()

	if len(regions) == 0 || dataSize == 0 {
		return &PrefetchStats{}
	}

	if config.PageSize <= 0 {
		config.PageSize = int64(unix.Getpagesize())
	}
	if config.GroupSize <= 0 {
		config.GroupSize = 1024
	}
	if config.MaxConcurrent <= 0 {
		config.MaxConcurrent = 8
	}

	stats := &PrefetchStats{}

	// Sort regions by priority (not modified in-place to respect caller).
	ordered := make([]PrefetchRegion, len(regions))
	copy(ordered, regions)
	// stable sort preserves insertion order within same priority.
	sortRegionsByPriority(ordered)

	// Count total pages.
	for _, r := range ordered {
		stats.TotalPages += uint64(r.Size / config.PageSize)
	}

	span.SetAttributes(
		attribute.Int64("prefetch.total_pages", int64(stats.TotalPages)),
		attribute.Int64("prefetch.page_size", config.PageSize),
		attribute.Int("prefetch.max_concurrent", config.MaxConcurrent),
		attribute.Int("prefetch.region_count", len(ordered)),
	)

	// Single shared channel for all work items. Priority ordering is
	// preserved because regions are sorted by priority before sending,
	// and the channel is FIFO — workers naturally process P0 items
	// before P1 items when there are enough workers to drain the buffer.
	workCh := make(chan workItem, 4096)

	// Fan out regions into work items.
	go func() {
		defer close(workCh)

		for _, r := range ordered {
			groupBytes := int64(config.GroupSize) * config.PageSize
			for off := r.Start; off < r.Start+r.Size; off += groupBytes {
				end := off + groupBytes
				if end > r.Start+r.Size {
					end = r.Start + r.Size
				}
				if end > dataSize {
					end = dataSize
				}
				if off >= end {
					break
				}

				select {
				case workCh <- workItem{start: off, end: end, priority: r.Priority}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup

	// Start workers.
	for range config.MaxConcurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.worker(ctx, data, workCh, stats)
		}()
	}

	wg.Wait()

	p.logger.Debug(ctx, "file-backend prefetch complete",
		zap.Uint64("total_pages", stats.TotalPages),
		zap.Uint64("faulted", stats.FaultedPages.Load()),
		zap.Uint64("skipped", stats.SkippedPages.Load()),
	)

	return stats
}

// worker consumes work items from the shared channel and pre-faults pages.
func (p *FileBackendPrefetcher) worker(
	ctx context.Context,
	data []byte,
	workCh <-chan workItem,
	stats *PrefetchStats,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case item, ok := <-workCh:
			if !ok {
				return
			}
			p.prefaultRange(data, item.start, item.end, stats)
		}
	}
}

// prefaultRange touches each page in [start, end) to trigger PTE
// establishment. For shared memfiles (MAP_PRIVATE), reads hit the
// host page cache; writes allocate CoW pages.
func (p *FileBackendPrefetcher) prefaultRange(
	data []byte,
	start, end int64,
	stats *PrefetchStats,
) {
	pageSize := int64(unix.Getpagesize())

	// Align start down and end up to page boundaries.
	pageStart := (start / pageSize) * pageSize
	pageEnd := ((end + pageSize - 1) / pageSize) * pageSize
	if pageEnd > int64(len(data)) {
		pageEnd = int64(len(data))
	}

	for off := pageStart; off < pageEnd; off += pageSize {
		// A single-byte read from each page forces the kernel to
		// establish a PTE. For shared MAP_PRIVATE pages this is a
		// minor fault (page cache hit, zero I/O). For pages not yet
		// in cache, the kernel reads them from the backing file.
		_ = data[off]
		stats.FaultedPages.Add(1)
	}
	stats.CompletedBatches.Add(1)
}

// sortRegionsByPriority performs a stable insertion sort by priority.
// Stable sort preserves insertion order within the same priority level,
// which matters for the ordered access sequence captured during build.
func sortRegionsByPriority(regions []PrefetchRegion) {
	for i := 1; i < len(regions); i++ {
		key := regions[i]
		j := i - 1
		for j >= 0 && regions[j].Priority > key.Priority {
			regions[j+1] = regions[j]
			j--
		}
		regions[j+1] = key
	}
}

// ── Agent-Aware Priority Builder ──────────────────────────────────────

// AgentPriorityBuilder constructs the ordered PrefetchRegion list for an
// OpenClaw Agent sandbox. It maps the known memory layout of a warmed-up
// Node.js + OpenClaw Gateway to priority tiers.
type AgentPriorityBuilder struct {
	// GatewayCodeRange is the guest-physical range containing the OpenClaw
	// Gateway compiled code (V8 code space, JIT output).
	GatewayCodeRange [2]int64

	// GatewayDataRange is the guest-physical range for Gateway heap data.
	GatewayDataRange [2]int64

	// NodeRuntimeRange is the guest-physical range for Node.js runtime.
	NodeRuntimeRange [2]int64

	// SystemRange is the guest-physical range for kernel/envd/base libs.
	SystemRange [2]int64
}

// Build returns the ordered prefetch regions for an Agent sandbox.
// P0: System pages (envd + kernel pages needed for socket readiness)
// P1: Node.js runtime + Gateway code (needed for first request)
// P2: Gateway data (heap, compiled regexps, inline caches)
// P3: Everything else (cold paths, infrequently used modules)
func (b *AgentPriorityBuilder) Build(totalSize int64) []PrefetchRegion {
	var regions []PrefetchRegion

	// P0: System / envd — VM must respond to health checks immediately.
	if b.SystemRange[1] > b.SystemRange[0] {
		regions = append(regions, PrefetchRegion{
			Start:    b.SystemRange[0],
			Size:     b.SystemRange[1] - b.SystemRange[0],
			Priority: PriorityP0,
		})
	}

	// P1: Node.js runtime + Gateway compiled code — needed for first request.
	if b.NodeRuntimeRange[1] > b.NodeRuntimeRange[0] {
		regions = append(regions, PrefetchRegion{
			Start:    b.NodeRuntimeRange[0],
			Size:     b.NodeRuntimeRange[1] - b.NodeRuntimeRange[0],
			Priority: PriorityP1,
		})
	}
	if b.GatewayCodeRange[1] > b.GatewayCodeRange[0] {
		regions = append(regions, PrefetchRegion{
			Start:    b.GatewayCodeRange[0],
			Size:     b.GatewayCodeRange[1] - b.GatewayCodeRange[0],
			Priority: PriorityP1,
		})
	}

	// P2: Gateway heap data — inline caches, regexp caches, etc.
	if b.GatewayDataRange[1] > b.GatewayDataRange[0] {
		regions = append(regions, PrefetchRegion{
			Start:    b.GatewayDataRange[0],
			Size:     b.GatewayDataRange[1] - b.GatewayDataRange[0],
			Priority: PriorityP2,
		})
	}

	return regions
}

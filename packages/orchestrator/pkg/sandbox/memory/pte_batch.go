//go:build linux

package memory

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sys/unix"
)

// ── PTE Batch Pre-Faulting (MADV_POPULATE_WRITE) ──────────────────────

// PrefaultPages uses MADV_POPULATE_WRITE to install PTEs for the given
// regions in a single kernel call per region. This is faster than
// touching each page individually because it avoids per-page fault
// handling overhead.
//
// For MAP_PRIVATE shared pages: existing PTEs are re-used (page cache
// hit). For CoW pages: new physical pages are allocated and their PTEs
// are installed.
//
// Requires Linux 5.14+ (MADV_POPULATE_WRITE).
func PrefaultPages(ctx context.Context, data []byte, regions []PrefetchRegion) error {
	ctx, span := prefetchTracer.Start(ctx, "prefault-pages-batch")
	defer span.End()

	span.SetAttributes(attribute.Int("prefault.region_count", len(regions)))

	for _, r := range regions {
		if r.Start < 0 || r.Start+r.Size > int64(len(data)) {
			continue
		}

		if err := unix.Madvise(data[r.Start:r.Start+r.Size], unix.MADV_POPULATE_WRITE); err != nil {
			span.RecordError(err)
			return err
		}
		pteBatchPrefaultPages.Add(ctx, r.Size/int64(unix.Getpagesize()))
	}
	return nil
}

// PrefaultEntireRegion is a convenience wrapper that prefaults an entire
// contiguous memory region in a single MADV_POPULATE_WRITE call. It is
// used by fc/process.go after layered snapshot resume to eliminate minor
// page faults before the VM executes its first instruction.
func PrefaultEntireRegion(ctx context.Context, data []byte, size int64) error {
	if len(data) == 0 || size <= 0 {
		return nil
	}
	if size > int64(len(data)) {
		size = int64(len(data))
	}
	pageSize := int64(unix.Getpagesize())
	alignedSize := (size / pageSize) * pageSize
	if alignedSize <= 0 {
		return nil
	}

	if err := unix.Madvise(data[:alignedSize], unix.MADV_POPULATE_WRITE); err != nil {
		return err
	}
	pteBatchPrefaultPages.Add(context.Background(), alignedSize/pageSize)
	return nil
}

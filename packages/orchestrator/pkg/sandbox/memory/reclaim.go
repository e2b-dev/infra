//go:build linux

package memory

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var reclaimTracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory/reclaim")

// OnAgentExit gracefully reclaims memory when an agent sandbox exits.
//
// Strategy:
//  1. Optional L2 checkpoint save: persists CoW overlay for future resumes.
//     This MUST happen before reclaiming dirty pages, because MADV_DONTNEED
//     frees the physical pages that SaveCheckpoint needs to read.
//  2. CoW'd private pages: MADV_DONTNEED — immediate reclaim on the overlay
//     mmap (not the shared base). These are MAP_SHARED copies in the overlay
//     file; MADV_DONTNEED frees their physical memory and causes subsequent
//     reads to fault from the backing file.
//  3. Shared pages: MADV_COLD — keep in page cache but de-prioritize for LRU
//     reclamation. Other VMs can still fault them back.
func OnAgentExit(
	ctx context.Context,
	sharedData []byte,
	cowOverlay *CoWOverlay,
	l2CheckpointPath string,
	logger logger.Logger,
) error {
	ctx, span := reclaimTracer.Start(ctx, "on-agent-exit")
	defer span.End()

	// Step 1: Save L2 checkpoint BEFORE reclaiming pages.
	// SaveCheckpoint reads dirty pages from overlayData; MADV_DONTNEED
	// would free those physical pages, producing a stale/zero checkpoint.
	if cowOverlay != nil && l2CheckpointPath != "" && cowOverlay.HasDirtyPages() {
		if err := cowOverlay.SaveCheckpoint(ctx, l2CheckpointPath); err != nil {
			logger.Warn(ctx, "failed to save L2 checkpoint",
				zap.String("path", l2CheckpointPath),
				zap.Error(err),
			)
		}
	}

	// Step 2: Reclaim CoW'd private pages (MADV_DONTNEED on overlay).
	if cowOverlay != nil && cowOverlay.HasDirtyPages() {
		if err := cowOverlay.ReclaimDirtyPages(); err != nil {
			logger.Warn(ctx, "failed to reclaim CoW pages",
				zap.Error(err),
			)
		}
	}

	// Step 3: De-prioritize shared pages (MADV_COLD).
	if sharedData != nil && len(sharedData) > 0 {
		if err := reclaimSharedPages(sharedData); err != nil {
			logger.Warn(ctx, "failed to de-prioritize shared pages",
				zap.Error(err),
			)
		}
	}

	return nil
}

// ReclaimDirtyPages uses MADV_DONTNEED on overlay pages that were modified
// by the VM (CoW copies). This immediately frees the physical pages backing
// these copies. Since the overlay is MAP_SHARED, MADV_DONTNEED causes
// subsequent reads to fault from the backing sparse file (which only has
// data for dirty pages; clean pages return zeros).
func (c *CoWOverlay) ReclaimDirtyPages() error {
	dirty, _ := c.tracker.Export()
	for r := range block.BitsetRanges(dirty, c.pageSize) {
		off := r.Start
		size := r.Size
		if off < 0 || off+size > int64(len(c.overlayData)) {
			continue
		}
		if err := unix.Madvise(c.overlayData[off:off+size], unix.MADV_DONTNEED); err != nil {
			return fmt.Errorf("madvise DONTNEED at [%d,%d): %w", off, off+size, err)
		}
		cowPagesReclaimed.Add(context.Background(), size/c.pageSize)
	}
	return nil
}

// reclaimSharedPages uses MADV_COLD on the entire shared mmap region.
// MADV_COLD deactivates pages (moves them to the inactive LRU list) so the
// kernel can reclaim them under memory pressure. Other VMs can still access
// them via page cache hits.
func reclaimSharedPages(data []byte) error {
	if err := unix.Madvise(data, unix.MADV_COLD); err != nil {
		return fmt.Errorf("madvise COLD: %w", err)
	}
	sharedPagesCold.Add(context.Background(), int64(len(data)))
	return nil
}

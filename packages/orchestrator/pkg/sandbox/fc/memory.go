package fc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bits-and-blooms/bitset"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// MemoryInfo returns the memory info for the sandbox.
// The dirty field represents mincore resident pages—essentially pages that were faulted in.
// The empty field represents pages that are *resident*, but also completely empty.
func (p *Process) MemoryInfo(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.memoryInfo(ctx, blockSize)
}

func (p *Process) DirtyMemory(ctx context.Context, blockSize int64) (*header.DiffMetadata, error) {
	return p.client.dirtyMemory(ctx, blockSize)
}

func (p *Process) ExportMemory(
	ctx context.Context,
	include *bitset.BitSet,
	cachePath string,
	blockSize int64,
) (*block.Cache, error) {
	pid, err := p.Pid()
	if err != nil {
		return nil, fmt.Errorf("failed to get pid: %w", err)
	}

	// Collect guest-physical ranges from the include bitmap.
	var guestRanges []block.Range
	for r := range block.BitsetRanges(include, blockSize) {
		guestRanges = append(guestRanges, r)
	}

	// Try memfd path first: if FC was built with memfd support, we can read
	// guest memory directly via pread and free hugepages incrementally with
	// PUNCH_HOLE instead of copying everything through process_vm_readv.
	memfdFd, memfdErr := findMemfd(pid)
	if memfdErr == nil {
		defer unix.Close(memfdFd)

		cache, err := block.NewCacheFromMemfd(ctx, blockSize, cachePath, memfdFd, guestRanges)
		if err != nil {
			return nil, fmt.Errorf("failed to export memory via memfd: %w", err)
		}

		logger.L().Info(ctx, "exported memory via memfd",
			zap.Int("pid", pid),
			zap.Int("ranges", len(guestRanges)),
		)

		return cache, nil
	}

	// Fallback: translate guest-physical offsets to host virtual addresses
	// and copy via process_vm_readv.
	m, err := p.client.memoryMapping(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory mappings: %w", err)
	}

	var remoteRanges []block.Range
	for _, r := range guestRanges {
		hostVirtRanges, err := m.GetHostVirtRanges(r.Start, r.Size)
		if err != nil {
			return nil, fmt.Errorf("failed to get host virt ranges: %w", err)
		}

		remoteRanges = append(remoteRanges, hostVirtRanges...)
	}

	cache, err := block.NewCacheFromProcessMemory(ctx, blockSize, cachePath, pid, remoteRanges)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return cache, nil
}

// findMemfd scans /proc/<pid>/fd/ for a memfd entry (used by FC builds with
// memfd-backed guest memory). Returns an owned fd the caller must close.
func findMemfd(pid int) (int, error) {
	fdDir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		return -1, fmt.Errorf("readdir %s: %w", fdDir, err)
	}

	bestFd := -1
	var bestSize int64

	for _, entry := range entries {
		link, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}

		if !strings.Contains(link, "memfd:") {
			continue
		}

		fd, err := unix.Open(filepath.Join(fdDir, entry.Name()), unix.O_RDWR, 0)
		if err != nil {
			continue
		}

		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			unix.Close(fd)
			continue
		}

		// Pick the largest memfd — that's the guest RAM.
		if st.Size > bestSize {
			if bestFd >= 0 {
				unix.Close(bestFd)
			}
			bestFd = fd
			bestSize = st.Size
		} else {
			unix.Close(fd)
		}
	}

	if bestFd < 0 {
		return -1, fmt.Errorf("no memfd found for pid %d", pid)
	}

	return bestFd, nil
}

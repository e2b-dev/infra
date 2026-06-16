//go:build linux

package memory

import (
	"context"
	"fmt"

	"github.com/prometheus/procfs"
	"golang.org/x/sys/unix"
)

// hugePageSize is the x86_64 transparent-hugepage size. MADV_COLLAPSE operates
// on hugepage-aligned, hugepage-sized ranges.
const hugePageSize = 2 * 1024 * 1024

type region struct {
	start uintptr
	end   uintptr
}

// CollapseSelf consolidates the current process's anonymous heap by migrating
// its scattered base pages into 2 MiB transparent hugepages. It is best-effort:
// 2 MiB windows that cannot be collapsed (empty or ineligible) are skipped, not
// reported as errors. Intended to run just before a snapshot so the consolidated
// layout is captured.
//
// Honors ctx: when the caller (the orchestrator's bounded POST /collapse) times
// out or disconnects, CollapseSelf stops issuing further madvise calls and
// returns the progress made so far, so envd does not keep collapsing after the
// orchestrator has moved on with pause.
func CollapseSelf(ctx context.Context) (Stats, error) {
	proc, err := procfs.Self()
	if err != nil {
		return Stats{}, fmt.Errorf("open self procfs: %w", err)
	}

	maps, err := proc.ProcMaps()
	if err != nil {
		return Stats{}, fmt.Errorf("read self maps: %w", err)
	}

	var s Stats
	for _, r := range anonRWRegions(maps) {
		// Caller gave up (timeout/disconnect): stop issuing madvise calls and
		// return the progress made so far. Reported uniformly via ctx.Err()
		// below, regardless of which region was interrupted.
		if ctx.Err() != nil {
			break
		}
		s.Regions++
		rs := collapseRange(ctx, r.start, r.end)
		s.Chunks += rs.Chunks
		s.Collapsed += rs.Collapsed
		s.Skipped += rs.Skipped
	}

	// nil on a complete run; the cancellation error if the caller gave up.
	return s, ctx.Err()
}

// anonRWRegions selects the anonymous (no backing file) read-write mappings from
// a parsed /proc/<pid>/maps — envd's Go heap arenas live here. A non-empty
// Pathname means the mapping is file-backed or a pseudo-mapping ([stack]/[heap]
// /[vdso]/…), neither of which is an anon heap arena.
func anonRWRegions(maps []*procfs.ProcMap) []region {
	var regions []region
	for _, m := range maps {
		if m.Perms == nil || !m.Perms.Read || !m.Perms.Write {
			continue
		}
		if m.Pathname != "" {
			continue
		}
		regions = append(regions, region{start: m.StartAddr, end: m.EndAddr})
	}

	return regions
}

// collapseRange makes [start, end) transparent-hugepage eligible regardless of
// the system THP mode — MADV_HUGEPAGE sets VM_HUGEPAGE (and clears
// VM_NOHUGEPAGE), so MADV_COLLAPSE is accepted even under THP=madvise — then
// collapses each fully-contained 2 MiB window. A whole-region MADV_COLLAPSE
// aborts on the first empty window, so we issue it per window and skip failures.
func collapseRange(ctx context.Context, start, end uintptr) Stats {
	var s Stats

	// Best-effort eligibility hint; ignore errors.
	_ = madvise(start, end-start, unix.MADV_HUGEPAGE)

	first := (start + hugePageSize - 1) &^ uintptr(hugePageSize-1) // round up to 2 MiB
	for c := first; c+hugePageSize <= end; c += hugePageSize {
		if ctx.Err() != nil {
			return s
		}
		s.Chunks++
		if err := madvise(c, hugePageSize, unix.MADV_COLLAPSE); err == nil {
			s.Collapsed++
		} else {
			s.Skipped++
		}
	}

	return s
}

func madvise(addr, length uintptr, advice int) error {
	if _, _, errno := unix.Syscall(unix.SYS_MADVISE, addr, length, uintptr(advice)); errno != 0 {
		return errno
	}

	return nil
}

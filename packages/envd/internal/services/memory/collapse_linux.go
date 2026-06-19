//go:build linux

package memory

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

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
	// successes = windows where MADV_COLLAPSE returned 0. That conflates real
	// migrations with already-huge no-ops; we split them below via the
	// AnonHugePages delta.
	var successes int
	before, okBefore := anonHugePagesBytes()
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
		successes += rs.Collapsed
		s.Skipped += rs.Skipped
	}
	after, okAfter := anonHugePagesBytes()

	// Attribute madvise successes to real migrations vs already-huge no-ops
	// using the AnonHugePages delta. MADV_COLLAPSE is synchronous, so the delta
	// reflects exactly the hugepages this run created.
	s.Collapsed, s.AlreadyHuge = splitCollapsed(successes, before, after, okBefore && okAfter)

	// nil on a complete run; the cancellation error if the caller gave up.
	return s, ctx.Err()
}

// splitCollapsed attributes MADV_COLLAPSE successes to real migrations vs
// already-huge no-ops using the AnonHugePages byte delta across the collapse.
// collapsed = windows whose pages were actually migrated this run (the delta in
// 2 MiB hugepages), alreadyHuge = the rest.
//
// When the delta could not be measured (measured=false, e.g. smaps_rollup
// unreadable) or AnonHugePages went backwards (after < before, e.g. concurrent
// THP teardown), it falls back to the pre-split meaning — all successes counted
// as collapsed — so a missing or noisy reading never misreports real work as a
// no-op. The migrated count is clamped to successes, since background THP
// activity could otherwise inflate the delta past what these madvise calls did.
func splitCollapsed(successes int, before, after uint64, measured bool) (collapsed, alreadyHuge int) {
	if !measured || after < before {
		return successes, 0
	}
	migrated := min(int((after-before)/hugePageSize), successes)

	return migrated, successes - migrated
}

// anonHugePagesBytes reads AnonHugePages from /proc/self/smaps_rollup. The bool
// is false when the field can't be read (no smaps_rollup / CONFIG_PROC_PAGE_MONITOR),
// so callers can fall back instead of treating a missing read as zero hugepages.
func anonHugePagesBytes() (uint64, bool) {
	f, err := os.Open("/proc/self/smaps_rollup")
	if err != nil {
		return 0, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "AnonHugePages:") {
			continue
		}
		fields := strings.Fields(line) // ["AnonHugePages:", "<kB>", "kB"]
		if len(fields) < 2 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0, false
		}

		return kb * 1024, true
	}

	return 0, false
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

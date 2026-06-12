//go:build linux

package memory

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
func CollapseSelf() (Stats, error) {
	f, err := os.Open("/proc/self/maps")
	if err != nil {
		return Stats{}, fmt.Errorf("open self maps: %w", err)
	}
	defer f.Close()

	regions, err := parseAnonRWRegions(f)
	if err != nil {
		return Stats{}, fmt.Errorf("parse self maps: %w", err)
	}

	var s Stats
	for _, r := range regions {
		s.Regions++
		rs := collapseRange(r.start, r.end)
		s.Chunks += rs.Chunks
		s.Collapsed += rs.Collapsed
		s.Skipped += rs.Skipped
	}

	return s, nil
}

// collapseRange makes [start, end) transparent-hugepage eligible regardless of
// the system THP mode — MADV_HUGEPAGE sets VM_HUGEPAGE (and clears
// VM_NOHUGEPAGE), so MADV_COLLAPSE is accepted even under THP=madvise — then
// collapses each fully-contained 2 MiB window. A whole-region MADV_COLLAPSE
// aborts on the first empty window, so we issue it per window and skip failures.
func collapseRange(start, end uintptr) Stats {
	var s Stats

	// Best-effort eligibility hint; ignore errors.
	_ = madvise(start, end-start, unix.MADV_HUGEPAGE)

	first := (start + hugePageSize - 1) &^ uintptr(hugePageSize-1) // round up to 2 MiB
	for c := first; c+hugePageSize <= end; c += hugePageSize {
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

// parseAnonRWRegions returns the anonymous (no backing file) read-write regions
// of a /proc/<pid>/maps stream — envd's Go heap arenas live here. It tolerates
// malformed lines by skipping them.
func parseAnonRWRegions(r io.Reader) ([]region, error) {
	var regions []region

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		// addr-range perms offset dev inode [pathname]
		if len(fields) < 5 {
			continue
		}

		perms := fields[1]
		if len(perms) < 2 || perms[0] != 'r' || perms[1] != 'w' {
			continue
		}

		// Anonymous == no pathname. A present 6th field (a file path, or a
		// pseudo-path like [stack]/[heap]) means it is not an anon heap arena.
		if len(fields) >= 6 && fields[5] != "" {
			continue
		}

		dash := strings.IndexByte(fields[0], '-')
		if dash <= 0 {
			continue
		}
		start, err := strconv.ParseUint(fields[0][:dash], 16, 64)
		if err != nil {
			continue
		}
		end, err := strconv.ParseUint(fields[0][dash+1:], 16, 64)
		// The upper-bound checks guard the uint64->uintptr narrowing below
		// (uintptr is 32-bit on 32-bit platforms); envd only runs on 64-bit
		// hosts, so they never trip there but keep CodeQL's conversion check
		// satisfied.
		if err != nil || end <= start || start > uint64(^uintptr(0)) || end > uint64(^uintptr(0)) {
			continue
		}

		regions = append(regions, region{start: uintptr(start), end: uintptr(end)})
	}

	return regions, sc.Err()
}

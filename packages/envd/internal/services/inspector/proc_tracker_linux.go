//go:build linux
// +build linux

package inspector

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// linuxProcTracker uses the cgroup v2 cgroup.procs files to track set
// membership and the kernel's soft-dirty page-tracking mechanism to
// detect in-memory writes by surviving long-lived processes.
//
// State machine:
//
//   Reset: snapshot current PIDs; for each, clear_refs=4.
//   Query: snapshot again; compare; if equal, scan soft-dirty bits.
//
// excludePID skips envd's own PID and its handler children (we exclude
// envd entirely because envd writes to its own VMAs in steady state and
// would force a "process changed" decision every turn).
type linuxProcTracker struct {
	mu               sync.Mutex
	cgroupPaths      []string
	excludeSelf      bool
	selfPID          int
	baseline         map[int]struct{}
	softDirtyOK      bool
	btfPresent       bool
}

// newProcTracker constructs a tracker that monitors PIDs in the given
// v2 cgroup directories. selfPID is excluded from tracking; pass 0 to
// disable self-exclusion (mainly for tests).
func newProcTracker(cgroupPaths []string, selfPID int) procTracker {
	t := &linuxProcTracker{
		cgroupPaths: append([]string(nil), cgroupPaths...),
		baseline:    map[int]struct{}{},
		excludeSelf: selfPID > 0,
		selfPID:     selfPID,
	}
	t.softDirtyOK = probeSoftDirty()
	t.btfPresent = fileExists("/sys/kernel/btf/vmlinux")
	return t
}

func (t *linuxProcTracker) Reset() (int, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	pids := t.snapshotLocked()
	if !t.softDirtyOK {
		t.baseline = pids
		return len(pids), false
	}

	// Best-effort clear; per-PID errors don't fail the reset.
	for pid := range pids {
		_ = os.WriteFile(fmt.Sprintf("/proc/%d/clear_refs", pid), []byte("4"), 0o200)
	}
	t.baseline = pids
	return len(pids), true
}

func (t *linuxProcTracker) Query() (bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	current := t.snapshotLocked()

	// Membership delta.
	if !sameSet(t.baseline, current) {
		return true, t.softDirtyOK
	}

	if !t.softDirtyOK {
		// Can't read soft-dirty; conservative answer is "unknown".
		return false, false
	}

	for pid := range current {
		dirty, err := pidHasDirtyPages(pid)
		if err != nil {
			// One PID's failure shouldn't crater the whole query — if
			// we can't read pagemap (race with exit, permission
			// flake), keep checking the others.
			continue
		}
		if dirty {
			return true, true
		}
	}
	return false, true
}

func (t *linuxProcTracker) SoftDirtySupported() bool { return t.softDirtyOK }
func (t *linuxProcTracker) BTFPresent() bool         { return t.btfPresent }
func (t *linuxProcTracker) Close() error             { return nil }

// snapshotLocked returns the union of cgroup.procs across configured
// cgroups, excluding the daemon's own PID and any descendant.
func (t *linuxProcTracker) snapshotLocked() map[int]struct{} {
	out := map[int]struct{}{}
	for _, root := range t.cgroupPaths {
		t.collectCgroupProcs(root, out)
	}
	if t.excludeSelf {
		delete(out, t.selfPID)
	}
	return out
}

// collectCgroupProcs walks `root` recursively, reading every
// `cgroup.procs` file and populating `out`. Errors at any level are
// swallowed: missing cgroups are not fatal.
func (t *linuxProcTracker) collectCgroupProcs(root string, out map[int]struct{}) {
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() != "cgroup.procs" {
			return nil
		}
		readCgroupProcs(path, out)
		return nil
	})
}

func readCgroupProcs(path string, out map[int]struct{}) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		pid, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
		if err != nil {
			continue
		}
		out[pid] = struct{}{}
	}
}

// pidHasDirtyPages walks /proc/PID/maps and short-circuits on the first
// soft-dirty bit it finds. Read-only / vdso / vsyscall mappings are
// skipped because they can't carry user writes anyway.
func pidHasDirtyPages(pid int) (bool, error) {
	mapsPath := fmt.Sprintf("/proc/%d/maps", pid)
	pagemapPath := fmt.Sprintf("/proc/%d/pagemap", pid)

	maps, err := os.Open(mapsPath)
	if err != nil {
		return false, err
	}
	defer maps.Close()

	pagemap, err := os.Open(pagemapPath)
	if err != nil {
		return false, err
	}
	defer pagemap.Close()

	scanner := bufio.NewScanner(maps)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		// Format: "55a1b1234000-55a1b1235000 rw-p ..."
		spaceIdx := strings.IndexByte(line, ' ')
		if spaceIdx < 0 {
			continue
		}
		dashIdx := strings.IndexByte(line[:spaceIdx], '-')
		if dashIdx < 0 {
			continue
		}

		start, err := strconv.ParseUint(line[:dashIdx], 16, 64)
		if err != nil {
			continue
		}
		end, err := strconv.ParseUint(line[dashIdx+1:spaceIdx], 16, 64)
		if err != nil {
			continue
		}

		// perms field is right after the first space, e.g. "rw-p".
		permsEnd := spaceIdx + 1 + 4
		if permsEnd > len(line) {
			continue
		}
		perms := line[spaceIdx+1 : permsEnd]
		// Skip non-writable VMAs (they can't carry user writes).
		if perms[1] != 'w' {
			continue
		}
		// Skip [vvar], [vsyscall], and similar special mappings.
		if i := strings.IndexByte(line, '['); i >= 0 {
			special := line[i:]
			if strings.HasPrefix(special, "[vvar") ||
				strings.HasPrefix(special, "[vsyscall") ||
				strings.HasPrefix(special, "[vdso") {
				continue
			}
		}

		dirty, err := scanVMASoftDirty(pagemap, start, end)
		if err != nil {
			return false, err
		}
		if dirty {
			return true, nil
		}
	}
	return false, scanner.Err()
}

// scanVMASoftDirty reads pagemap entries for [start, end) and returns
// true if any entry has bit 55 set (soft-dirty). Pages are read in
// 4 KiB increments matching the standard architecture page size; that
// is also what the kernel uses to index pagemap regardless of huge
// pages, so this is correct for the kernels we target (5.x+).
func scanVMASoftDirty(pagemap *os.File, start, end uint64) (bool, error) {
	const pageSize = 4096
	const entrySize = 8

	pages := (end - start) / pageSize
	if pages == 0 {
		return false, nil
	}

	offset := int64(start / pageSize * entrySize)

	// Read in chunks of up to 1 MiB worth of entries (~128 KiB) to
	// avoid huge allocations on big VMAs.
	const chunkPages = 1 << 14
	buf := make([]byte, chunkPages*entrySize)

	remaining := pages
	for remaining > 0 {
		want := remaining
		if want > chunkPages {
			want = chunkPages
		}
		n, err := pagemap.ReadAt(buf[:want*entrySize], offset)
		if err != nil {
			// EOF / short read can happen on sparse VMAs; treat as
			// "no dirty pages found in this region".
			if errors.Is(err, io.EOF) || isShortRead(err) {
				return scanReadEntries(buf[:n]), nil
			}
			return false, err
		}
		if scanReadEntries(buf[:n]) {
			return true, nil
		}
		offset += int64(n)
		remaining -= uint64(n / entrySize)
		if uint64(n) < want*entrySize {
			break
		}
	}
	return false, nil
}

func isShortRead(err error) bool {
	return errors.Is(err, fs.ErrInvalid) || strings.Contains(err.Error(), "short read")
}

func scanReadEntries(buf []byte) bool {
	for i := 0; i+8 <= len(buf); i += 8 {
		entry := binary.LittleEndian.Uint64(buf[i : i+8])
		// Bit 55 = soft-dirty per Documentation/admin-guide/mm/soft-dirty.rst
		if entry&(1<<55) != 0 {
			return true
		}
	}
	return false
}

func sameSet(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// probeSoftDirty checks whether the kernel exposes the soft-dirty
// reset operation by writing "4" to /proc/self/clear_refs. Older
// kernels return EINVAL.
func probeSoftDirty() bool {
	err := os.WriteFile("/proc/self/clear_refs", []byte("4"), 0o200)
	return err == nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

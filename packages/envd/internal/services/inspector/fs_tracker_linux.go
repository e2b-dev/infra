//go:build linux && inspector_bpf
// +build linux,inspector_bpf

package inspector

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

// linuxFsTracker is the real eBPF-backed tracker. Built only when the
// inspector_bpf tag is set; otherwise stubFsTracker is used.
type linuxFsTracker struct {
	mu      sync.Mutex
	objs    fstrackerObjects
	links   []link.Link
	loaded  bool
	closed  bool
}

func newFsTracker() fsTracker { return &linuxFsTracker{} }

// Start lifts MEMLOCK, loads the embedded BPF object, and attaches the
// programs to their tracepoints. Errors leave the tracker in a degraded
// state: subsequent Query / Reset return ok=false.
func (t *linuxFsTracker) Start(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return errors.New("fs tracker closed")
	}
	if t.loaded {
		return nil
	}

	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("rlimit memlock: %w", err)
	}

	if err := loadFstrackerObjects(&t.objs, nil); err != nil {
		return fmt.Errorf("load fstracker objects: %w", err)
	}

	type binding struct {
		tracepoint string
		prog       *ebpf.Program
	}
	bindings := []binding{
		{"sys_enter_openat", t.objs.HandleOpenat},
		{"sys_enter_openat2", t.objs.HandleOpenat2},
		{"sys_enter_unlinkat", t.objs.HandleUnlinkat},
		{"sys_enter_renameat2", t.objs.HandleRenameat2},
		{"sys_enter_write", t.objs.HandleWrite},
		{"sys_enter_pwrite64", t.objs.HandlePwrite64},
		{"sys_enter_writev", t.objs.HandleWritev},
		{"sys_enter_pwritev2", t.objs.HandlePwritev2},
		{"sys_enter_truncate", t.objs.HandleTruncate},
		{"sys_enter_ftruncate", t.objs.HandleFtruncate},
		{"sys_enter_mkdirat", t.objs.HandleMkdirat},
		{"sys_enter_linkat", t.objs.HandleLinkat},
		{"sys_enter_symlinkat", t.objs.HandleSymlinkat},
		{"sys_enter_fallocate", t.objs.HandleFallocate},
		{"sys_enter_fchmodat", t.objs.HandleFchmodat},
		{"sys_enter_fchownat", t.objs.HandleFchownat},
	}

	for _, b := range bindings {
		l, err := link.Tracepoint("syscalls", b.tracepoint, b.prog, nil)
		if err != nil {
			t.unlinkUnlocked()
			_ = t.objs.Close()
			return fmt.Errorf("attach tracepoint %s: %w", b.tracepoint, err)
		}
		t.links = append(t.links, l)
	}

	t.loaded = true
	return nil
}

func (t *linuxFsTracker) AddCgroup(cgroupID uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		return nil
	}
	one := uint8(1)
	return t.objs.CgroupIdFilter.Update(&cgroupID, &one, ebpf.UpdateAny)
}

func (t *linuxFsTracker) RemoveCgroup(cgroupID uint64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		return nil
	}
	if err := t.objs.CgroupIdFilter.Delete(&cgroupID); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
		return err
	}
	return nil
}

func (t *linuxFsTracker) Query() (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		return 0, false
	}
	var key uint32
	var val uint64
	if err := t.objs.ChangeCounters.Lookup(&key, &val); err != nil {
		return 0, false
	}
	return val, true
}

func (t *linuxFsTracker) Reset() (uint64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.loaded {
		return 0, false
	}
	var key uint32
	var val uint64
	if err := t.objs.ChangeCounters.Lookup(&key, &val); err != nil {
		return 0, false
	}
	zero := uint64(0)
	if err := t.objs.ChangeCounters.Update(&key, &zero, ebpf.UpdateAny); err != nil {
		return val, false
	}
	return val, true
}

func (t *linuxFsTracker) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	if !t.loaded {
		return nil
	}
	t.unlinkUnlocked()
	t.loaded = false
	return t.objs.Close()
}

func (t *linuxFsTracker) unlinkUnlocked() {
	for _, l := range t.links {
		_ = l.Close()
	}
	t.links = nil
}

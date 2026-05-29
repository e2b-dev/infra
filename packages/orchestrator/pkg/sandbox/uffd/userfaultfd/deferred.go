//go:build linux

package userfaultfd

import "sync"

// deferredFaults collects pagefaults that returned EAGAIN so they get
// retried on the next poll iteration. Safe for concurrent push.
type deferredFaults struct {
	mu     sync.Mutex
	pf     []*UffdPagefault
	byAddr map[uint64]struct{}
}

func (d *deferredFaults) push(pf *UffdPagefault) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byAddr == nil {
		d.byAddr = make(map[uint64]struct{})
	}
	if _, ok := d.byAddr[pf.address]; ok {
		return false
	}
	d.byAddr[pf.address] = struct{}{}
	d.pf = append(d.pf, pf)

	return true
}

func (d *deferredFaults) drain() []*UffdPagefault {
	d.mu.Lock()
	out := d.pf
	d.pf = nil
	clear(d.byAddr)
	d.mu.Unlock()

	return out
}

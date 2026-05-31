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

// push queues a deferred fault, skipping addresses already queued so a page
// faulted by several threads is retried once instead of once per fault.
func (d *deferredFaults) push(pf *UffdPagefault) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byAddr == nil {
		d.byAddr = make(map[uint64]struct{})
	}
	addr := uint64(pf.address)
	if _, ok := d.byAddr[addr]; ok {
		return
	}
	d.byAddr[addr] = struct{}{}
	d.pf = append(d.pf, pf)
}

func (d *deferredFaults) drain() []*UffdPagefault {
	d.mu.Lock()
	out := d.pf
	d.pf = nil
	d.byAddr = nil
	d.mu.Unlock()

	return out
}

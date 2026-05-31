//go:build linux

package userfaultfd

import "sync"

// deferredFaults collects pagefaults that returned EAGAIN so they get
// retried on the next poll iteration. Safe for concurrent push.
type deferredFaults struct {
	mu     sync.Mutex
	pf     []*UffdPagefault
	byAddr map[uint64]*UffdPagefault
}

// push queues a deferred fault, skipping addresses already queued so a page
// faulted by several threads is retried once instead of once per fault.
// Fault addresses are already page-aligned by the kernel (UFFDIO_COPY rejects
// unaligned dst), so the raw address keys per page. If the same page is faulted
// as both read and write, the retained fault is upgraded to write so the retry
// installs it dirty instead of leaving a later WP fault to catch it.
func (d *deferredFaults) push(pf *UffdPagefault) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byAddr == nil {
		d.byAddr = make(map[uint64]*UffdPagefault)
	}
	addr := uint64(pf.address)
	if existing, ok := d.byAddr[addr]; ok {
		if pf.flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			existing.flags |= UFFD_PAGEFAULT_FLAG_WRITE
		}

		return
	}
	d.byAddr[addr] = pf
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

//go:build linux

package userfaultfd

import "sync"

// deferredFaults collects pagefaults that returned EAGAIN so they get
// retried on the next poll iteration. Safe for concurrent push.
type deferredFaults struct {
	mu    sync.Mutex
	pf    []*UffdPagefault
	byKey map[deferredKey]*UffdPagefault
}

// deferredKey dedupes deferred faults per (page, fault kind). WP faults must
// not share an entry with MISSING faults for the same page: their resolutions
// differ (unprotect+wake vs install), and folding a WP fault into a MISSING
// entry would drop the unprotect — the MISSING retry can short-circuit on an
// already-present page without waking, leaving the WP-blocked writer stranded.
// Retrying both kinds independently is safe: each resolution is idempotent
// and wakes its own waiters.
type deferredKey struct {
	addr uint64
	wp   bool
}

// push queues a deferred fault, skipping (page, kind) pairs already queued so
// a page faulted by several threads is retried once per kind instead of once
// per fault. Fault addresses are already page-aligned by the kernel
// (UFFDIO_COPY rejects unaligned dst), so the raw address keys per page. If
// the same page is missing-faulted as both read and write, the retained fault
// is upgraded to write so the retry installs it dirty instead of leaving a
// later WP fault to catch it.
func (d *deferredFaults) push(pf *UffdPagefault) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.byKey == nil {
		d.byKey = make(map[deferredKey]*UffdPagefault)
	}
	key := deferredKey{
		addr: uint64(pf.address),
		wp:   pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0,
	}
	if existing, ok := d.byKey[key]; ok {
		if pf.flags&UFFD_PAGEFAULT_FLAG_WRITE != 0 {
			existing.flags |= UFFD_PAGEFAULT_FLAG_WRITE
		}

		return
	}
	d.byKey[key] = pf
	d.pf = append(d.pf, pf)
}

func (d *deferredFaults) drain() []*UffdPagefault {
	d.mu.Lock()
	out := d.pf
	d.pf = nil
	d.byKey = nil
	d.mu.Unlock()

	return out
}

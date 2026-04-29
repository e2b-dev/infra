package userfaultfd

import "sync"

// deferredFaults collects pagefaults that couldn't be handled (EAGAIN)
// and need to be retried on the next poll iteration. Safe for concurrent push.
type deferredFaults struct {
	mu sync.Mutex
	pf []*UffdPagefault
}

func (d *deferredFaults) push(pf *UffdPagefault) {
	d.mu.Lock()
	d.pf = append(d.pf, pf)
	d.mu.Unlock()
}

// drain returns all accumulated pagefaults and resets the internal list.
func (d *deferredFaults) drain() []*UffdPagefault {
	d.mu.Lock()
	out := d.pf
	d.pf = nil
	d.mu.Unlock()

	return out
}

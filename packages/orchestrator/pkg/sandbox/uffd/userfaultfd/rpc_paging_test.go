package userfaultfd

// Paging RPC service. Exposes the per-page state snapshot consumed
// by tests asserting which offsets the handler observed, plus the
// pause/resume handles used by gated tests to drain the kernel's
// UFFD event queue deterministically.

import (
	"errors"
	"fmt"
)

// pageStateEntry is the wire format for the Paging.States RPC.
type pageStateEntry struct {
	State  uint8
	Offset uint64
}

type PageStatesReply struct {
	Entries []pageStateEntry
}

// Paging is the RPC service exposing page-state introspection and
// the gated-serve pause/resume controls.
type Paging struct {
	state *harnessState
}

func (p *Paging) States(_ *Empty, reply *PageStatesReply) error {
	p.state.mu.Lock()
	uffd := p.state.uffd
	p.state.mu.Unlock()
	if uffd == nil {
		return errors.New("Paging.States called before Lifecycle.Bootstrap")
	}

	entries, err := uffd.pageStateEntries()
	if err != nil {
		return err
	}
	reply.Entries = entries

	return nil
}

func (p *Paging) Pause(_ *Empty, _ *Empty) error {
	p.state.stopServe()

	return nil
}

func (p *Paging) Resume(_ *Empty, _ *Empty) error {
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	p.state.startServeLocked()

	return nil
}

// pageStateEntries returns a snapshot of every tracked page and its
// state. It briefly takes settleRequests.Lock so no in-flight worker
// can mutate the pageTracker while we read it.
func (u *Userfaultfd) pageStateEntries() ([]pageStateEntry, error) {
	u.settleRequests.Lock()
	u.settleRequests.Unlock() //nolint:staticcheck // SA2001: intentional — settle the read locks.

	u.pageTracker.mu.RLock()
	defer u.pageTracker.mu.RUnlock()

	entries := make([]pageStateEntry, 0, len(u.pageTracker.m))
	for addr, state := range u.pageTracker.m {
		offset, err := u.ma.GetOffset(addr)
		if err != nil {
			return nil, fmt.Errorf("address %#x not in mapping: %w", addr, err)
		}
		entries = append(entries, pageStateEntry{State: uint8(state), Offset: uint64(offset)})
	}

	return entries, nil
}

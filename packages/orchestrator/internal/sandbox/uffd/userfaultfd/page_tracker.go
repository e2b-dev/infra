package userfaultfd

import (
	"sync"
)

type pageState int

const (
	unfaulted pageState = iota
	faulted
	removed
)

// pageTracker is a concurrent map tracking the state of guest memory pages,
// keyed by host virtual address. Pages not yet recorded are implicitly
// unfaulted.
//
// Uses sync.Map because the access pattern is predominantly read-heavy
// with disjoint key access across goroutines.
type pageTracker struct {
	pageSize uintptr
	m        sync.Map // map[uintptr]pageState
}

func newPageTracker(pageSize uintptr) pageTracker {
	return pageTracker{pageSize: pageSize}
}

func (pt *pageTracker) get(addr uintptr) pageState {
	v, ok := pt.m.Load(addr)
	if !ok {
		return unfaulted
	}

	return v.(pageState)
}

func (pt *pageTracker) setState(state pageState, start, end uintptr) {
	for addr := start; addr < end; addr += pt.pageSize {
		pt.m.Store(addr, state)
	}
}

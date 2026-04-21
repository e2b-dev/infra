package userfaultfd

import "sync"

type pageState uint8

const (
	// unfaulted is the implicit state for pages not yet present in the
	// tracker map; it is the zero value of pageState.
	unfaulted pageState = iota
	faulted
	removed
)

type pageTracker struct {
	pageSize uintptr

	m  map[uintptr]pageState
	mu sync.RWMutex
}

func newPageTracker(pageSize uintptr) *pageTracker {
	return &pageTracker{
		pageSize: pageSize,
		m:        make(map[uintptr]pageState),
	}
}

func (pt *pageTracker) get(addr uintptr) pageState {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	state, ok := pt.m[addr]
	if !ok {
		return unfaulted
	}

	return state
}

func (pt *pageTracker) setState(start, end uintptr, state pageState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	for addr := start; addr < end; addr += pt.pageSize {
		pt.m[addr] = state
	}
}

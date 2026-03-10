package userfaultfd

import "sync"

type pageState uint8

const (
	unfaulted pageState = iota
	faulted
	removed
)

type pageTracker struct {
	pageSize uintptr

	m        map[uintptr]pageState
	versions map[uintptr]uint64
	mu       sync.RWMutex
}

func newPageTracker(pageSize uintptr) pageTracker {
	return pageTracker{
		pageSize: pageSize,
		m:        make(map[uintptr]pageState),
		versions: make(map[uintptr]uint64),
	}
}

func (pt *pageTracker) get(addr uintptr) pageState {
	state, _ := pt.getWithVersion(addr)

	return state
}

func (pt *pageTracker) getWithVersion(addr uintptr) (pageState, uint64) {
	pt.mu.RLock()
	defer pt.mu.RUnlock()

	state, ok := pt.m[addr]
	if !ok {
		return unfaulted, pt.versions[addr]
	}

	return state, pt.versions[addr]
}

func (pt *pageTracker) setState(start, end uintptr, state pageState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	for addr := start; addr < end; addr += pt.pageSize {
		pt.m[addr] = state
		pt.versions[addr]++
	}
}

func (pt *pageTracker) setStateIfVersion(addr uintptr, state pageState, version uint64) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	if pt.versions[addr] != version {
		return false
	}

	pt.m[addr] = state
	pt.versions[addr]++

	return true
}

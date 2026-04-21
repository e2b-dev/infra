package userfaultfd

import "sync"

type pageState uint8

const (
	// faulted starts at 1 so the pageState zero value is reserved for pages
	// not yet present in the tracker map.
	faulted pageState = iota + 1
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

func (pt *pageTracker) setState(start, end uintptr, state pageState) {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	for addr := start; addr < end; addr += pt.pageSize {
		pt.m[addr] = state
	}
}

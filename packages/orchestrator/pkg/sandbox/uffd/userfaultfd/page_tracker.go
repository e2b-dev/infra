package userfaultfd

import "sync"

type pageState uint8

const (
	unfaulted pageState = iota
	faulted
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

package utils

import (
	"sync"
)

type AtomicMax struct {
	val int64
	mu  sync.Mutex
}

func NewAtomicMax() *AtomicMax {
	return &AtomicMax{}
}

func (a *AtomicMax) Compare(newValue int64) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.val > newValue {
		return false
	}

	a.val = newValue
	return true
}

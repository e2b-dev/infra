package utils

import (
	"sync"
)

// WaitMap allows you to wait for functions with given keys and execute them
// only once. Consider replacing with singleflight if appropriate.
type WaitMap struct {
	mu sync.Mutex
	m  map[int64]func() error
}

func NewWaitMap() *WaitMap {
	return &WaitMap{
		m: make(map[int64]func() error),
	}
}

// Wait waits for the function with the given key to be executed.
// If the function is already executing, it waits for it to finish.
// If the function is not yet executing, it executes the function and returns its result.
func (m *WaitMap) Wait(key int64, fn func() error) error {
	m.mu.Lock()

	once, ok := m.m[key]
	if ok {
		m.mu.Unlock()

		return once()
	}

	once = sync.OnceValue(fn)

	m.m[key] = once

	m.mu.Unlock()

	return once()
}

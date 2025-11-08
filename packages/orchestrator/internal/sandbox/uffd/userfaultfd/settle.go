package userfaultfd

import "sync"

type settle struct {
	mu sync.RWMutex
}

func (s *settle) Add() {
	s.mu.RLock()
}

func (s *settle) Remove() {
	s.mu.RUnlock()
}

func (s *settle) Wait() {
	s.mu.Lock()
	s.mu.Unlock()
}

package utils

import (
	"context"
	"sync"
)

type SettleCounter struct {
	mu          sync.Mutex
	cond        *sync.Cond
	counter     int64
	settleValue int64
}

func NewZeroSettleCounter() *SettleCounter {
	c := &SettleCounter{settleValue: 0}

	c.cond = sync.NewCond(&c.mu)

	return c
}

func (s *SettleCounter) add(delta int64) {
	s.mu.Lock()

	s.counter += delta

	if s.counter == s.settleValue {
		s.cond.Broadcast() // wake up all waiters
	}

	s.mu.Unlock()
}

func (s *SettleCounter) Add() {
	s.add(1)
}

func (s *SettleCounter) Done() {
	s.add(-1)
}

func (s *SettleCounter) Wait(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// fast path
	if s.counter == s.settleValue {
		return nil
	}

	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.cond.Broadcast() // wake waiters to check ctx
			s.mu.Unlock()
		case <-done:
		}
	}()

	for s.counter != s.settleValue {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		s.cond.Wait()
	}

	return nil
}

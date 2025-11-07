package utils

import (
	"context"
	"sync"
	"sync/atomic"
)

type SettleCounter struct {
	counter     atomic.Int64
	cond        sync.Cond
	settleValue int64
}

// NewZeroSettleCounter creates a new SettleCounter that settles when the counter is zero.
func NewZeroSettleCounter() *SettleCounter {
	return &SettleCounter{
		counter:     atomic.Int64{},
		cond:        *sync.NewCond(&sync.Mutex{}),
		settleValue: 0,
	}
}

func (w *SettleCounter) add(delta int64) {
	if w.counter.Add(delta) == w.settleValue {
		w.cond.Broadcast()
	}
}

func (w *SettleCounter) Add() {
	w.add(1)
}

func (w *SettleCounter) Done() {
	w.add(-1)
}

// Wait waits for the counter to be the settle value.
func (w *SettleCounter) Wait(ctx context.Context) error {
	// Ensure we can break out of this Wait when the context is done.
	go func() {
		<-ctx.Done()

		w.cond.Broadcast()
	}()

	for w.counter.Load() != w.settleValue {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		w.cond.L.Lock()

		w.cond.Wait()

		w.cond.L.Unlock()
	}

	return nil
}

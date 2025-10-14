package utils

import (
	"context"
	"sync"
	"sync/atomic"
)

type WaitCounter struct {
	counter atomic.Int64
	cond    sync.Cond
}

func (w *WaitCounter) add(delta int64) {
	if w.counter.Add(delta) == 0 {
		w.cond.Broadcast()
	}
}

func (w *WaitCounter) Add() {
	w.add(1)
}

func (w *WaitCounter) Done() {
	w.add(-1)
}

func (w *WaitCounter) Wait(ctx context.Context) error {
	// Ensure we can break out of the loop when the context is done.
	go func() {
		<-ctx.Done()

		w.cond.Broadcast()
	}()

	for w.counter.Load() != 0 {
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

func (w *WaitCounter) Close() {
	w.counter.Store(0)

	w.cond.Broadcast()
}

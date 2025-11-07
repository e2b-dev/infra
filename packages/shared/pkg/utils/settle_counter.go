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
	stop := context.AfterFunc(ctx, w.cond.Broadcast)
	defer stop()

	for w.counter.Load() != w.settleValue {
		if err := ctx.Err(); err != nil {
			return err
		}

		w.cond.L.Lock()
		w.cond.Wait()
		w.cond.L.Unlock()
	}

	return nil
}

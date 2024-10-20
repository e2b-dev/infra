package utils

import (
	"context"
	"sync"
	"time"
)

type LockableCancelableContext struct {
	ctx    context.Context
	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewLockableCancelableContext(ctx context.Context) *LockableCancelableContext {
	lcc := &LockableCancelableContext{}
	lcc.ctx, lcc.cancel = context.WithCancel(ctx)
	return lcc
}

func (lcc *LockableCancelableContext) Lock() {
	lcc.mu.Lock()
}

func (lcc *LockableCancelableContext) Unlock() {
	lcc.mu.Unlock()
}

func (lcc *LockableCancelableContext) Done() <-chan struct{} {
	return lcc.ctx.Done()
}

func (lcc *LockableCancelableContext) Err() error {
	return lcc.ctx.Err()
}

func (lcc *LockableCancelableContext) Value(key interface{}) interface{} {
	return lcc.ctx.Value(key)
}

func (lcc *LockableCancelableContext) Cancel() {
	lcc.cancel()
}

func (lcc *LockableCancelableContext) Deadline() (deadline time.Time, ok bool) {
	return lcc.ctx.Deadline()
}

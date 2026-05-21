package ratelimit

import (
	"sync/atomic"
	"time"
)

// Limiter gates a recurring log to at most one emit per `floor`, counting
// suppressed attempts in between. The caller decides how to format/emit;
// this type only owns the gating decision.
type Limiter struct {
	floor      time.Duration
	lastLogged atomic.Pointer[time.Time]
	suppressed atomic.Int64
}

func New(floor time.Duration) *Limiter {
	return &Limiter{floor: floor}
}

// Allow returns (true, suppressedSinceLast) when the caller should emit a
// log line; false otherwise. On true the caller should include
// `suppressedSinceLast` in the emitted message.
func (r *Limiter) Allow() (bool, int64) {
	last := r.lastLogged.Load()
	if last != nil && time.Since(*last) <= r.floor {
		r.suppressed.Add(1)

		return false, 0
	}
	now := time.Now()
	if !r.lastLogged.CompareAndSwap(last, &now) {
		r.suppressed.Add(1)

		return false, 0
	}

	return true, r.suppressed.Swap(0)
}

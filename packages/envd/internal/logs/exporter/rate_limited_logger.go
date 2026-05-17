package exporter

import (
	"log"
	"sync/atomic"
	"time"
)

// rateLimitedLogger emits log.Printf at most once per floor, appending a
// count of suppressed calls since the last emitted line. It is lock-free
// and may slightly miscount under contention (acceptable for noise caps).
type rateLimitedLogger struct {
	floor      time.Duration
	format     string
	lastLogged atomic.Int64 // unix nano
	suppressed atomic.Int64
}

func newRateLimitedLogger(floor time.Duration, format string) *rateLimitedLogger {
	return &rateLimitedLogger{floor: floor, format: format}
}

func (r *rateLimitedLogger) log(args ...any) {
	last := r.lastLogged.Load()
	now := time.Now().UnixNano()
	if now-last <= int64(r.floor) || !r.lastLogged.CompareAndSwap(last, now) {
		r.suppressed.Add(1)
		return
	}
	suppressed := r.suppressed.Swap(0)
	log.Printf(r.format+" (%d suppressed since last log)", append(args, suppressed)...)
}

package exporter

import (
	"log"
	"sync/atomic"
	"time"
)

type rateLimitedLogger struct {
	floor      time.Duration
	format     string
	lastLogged atomic.Pointer[time.Time]
	suppressed atomic.Int64
}

func newRateLimitedLogger(floor time.Duration, format string) *rateLimitedLogger {
	return &rateLimitedLogger{floor: floor, format: format}
}

func (r *rateLimitedLogger) log(args ...any) {
	last := r.lastLogged.Load()
	if last != nil && time.Since(*last) <= r.floor {
		r.suppressed.Add(1)
		return
	}
	now := time.Now()
	if !r.lastLogged.CompareAndSwap(last, &now) {
		r.suppressed.Add(1)
		return
	}
	suppressed := r.suppressed.Swap(0)
	log.Printf(r.format+" (%d suppressed since last log)", append(args, suppressed)...)
}

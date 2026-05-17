package exporter

import (
	"log"
	"sync"
	"time"
)

type rateLimitedLogger struct {
	floor  time.Duration
	format string

	mu         sync.Mutex
	lastLogged time.Time
	suppressed int
}

func newRateLimitedLogger(floor time.Duration, format string) *rateLimitedLogger {
	return &rateLimitedLogger{floor: floor, format: format}
}

func (r *rateLimitedLogger) log(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastLogged) <= r.floor {
		r.suppressed++
		return
	}
	log.Printf(r.format+" (%d suppressed since last log)", append(args, r.suppressed)...)
	r.lastLogged = time.Now()
	r.suppressed = 0
}

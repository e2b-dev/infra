package exporter

import (
	"log"

	"github.com/e2b-dev/infra/packages/envd/internal/logs/ratelimit"
)

type rateLimitedLogger struct {
	limit  *ratelimit.Limiter
	format string
}

func newRateLimitedLogger(format string) *rateLimitedLogger {
	return &rateLimitedLogger{limit: ratelimit.New(logFloor), format: format}
}

func (r *rateLimitedLogger) log(args ...any) {
	ok, suppressed := r.limit.Allow()
	if !ok {
		return
	}
	log.Printf(r.format+" (%d suppressed since last log)", append(args, suppressed)...)
}

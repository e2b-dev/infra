package redis

import (
	"math/rand"
	"time"
)

type constantBackoff struct {
	backoff time.Duration
}

func newConstantBackoff(backoff time.Duration) *constantBackoff {
	return &constantBackoff{backoff: backoff}
}

const jitter = 0.25 // ±25%

// NextBackoff returns the base backoff duration with +/- 25% random jitter applied.
//
// The jitter prevents multiple competing goroutines from retrying in lockstep.
//
// Without jitter, concurrent goroutines using a constant 20ms backoff would retry
// simultaneously, hitting the Redis lock at the same time on every attempt. The
// randomization breaks this synchronization pattern while maintaining fast retries.
func (b *constantBackoff) NextBackoff() time.Duration {
	factor := 1 + jitter*(2*(rand.Float64()-0.5))

	return time.Duration(float64(b.backoff) * factor)
}

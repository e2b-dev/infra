package redis

import (
	"math/rand"
	"time"
)

type ConstantBackoff struct {
	backoff time.Duration
}

func NewConstantBackoff(backoff time.Duration) *ConstantBackoff {
	return &ConstantBackoff{backoff: backoff}
}

const jitter = 0.25 // ±25%

func (b *ConstantBackoff) NextBackoff() time.Duration {
	factor := 1 + jitter*(2*(rand.Float64()-0.5))

	return time.Duration(float64(b.backoff) * factor)
}

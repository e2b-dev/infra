package userfaultfd

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
)

// chaosSource wraps a block.Slicer and injects a uniform-random [0, maxDelay]
// latency before each Slice call. The RNG is seeded so failing runs can be
// reproduced verbatim: set UFFD_CHAOS_SEED=<seed> to replay, or pass the seed
// explicitly in tests.
type chaosSource struct {
	inner    block.Slicer
	rng      *rand.Rand //nolint:gosec // math/rand is fine; reproducibility > security
	rngMu    sync.Mutex
	maxDelay time.Duration
}

// newChaosSource creates a chaosSource wrapping inner. Every Slice call sleeps
// for a uniform-random duration in [0, maxDelay] before forwarding to inner.
// The sequence is fully deterministic for a given seed.
func newChaosSource(inner block.Slicer, seed int64, maxDelay time.Duration) *chaosSource {
	return &chaosSource{
		inner:    inner,
		rng:      rand.New(rand.NewSource(seed)), //nolint:gosec
		maxDelay: maxDelay,
	}
}

// Slice sleeps for a random delay in [0, maxDelay], then forwards to inner.
// Returns ctx.Err() immediately if the context is already cancelled or
// becomes cancelled during the sleep.
func (c *chaosSource) Slice(ctx context.Context, off int64, sz int64) ([]byte, error) {
	c.rngMu.Lock()
	d := time.Duration(c.rng.Int63n(int64(c.maxDelay) + 1))
	c.rngMu.Unlock()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(d):
	}

	return c.inner.Slice(ctx, off, sz)
}

// BlockSize forwards to inner.
func (c *chaosSource) BlockSize() int64 { return c.inner.BlockSize() }

package limit

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

// AcquireUploadSlot reserves a slot in the shared storage-upload semaphore and
// returns a func releasing it. Safe on a nil receiver: without a limiter the
// upload is not throttled and release is a no-op.
func (l *Limiter) AcquireUploadSlot(ctx context.Context) (release func(), err error) {
	if l == nil {
		return func() {}, nil
	}

	if err := l.storageUploadLimiter.Acquire(ctx, 1); err != nil {
		return nil, fmt.Errorf("failed to acquire semaphore: %w", err)
	}

	return func() { l.storageUploadLimiter.Release(1) }, nil
}

// MaxUploadTasks returns the per-upload concurrency limit. Safe on a nil
// receiver: without a limiter it returns the flag's fallback value.
func (l *Limiter) MaxUploadTasks(ctx context.Context) int {
	if l == nil {
		return featureflags.StorageMaxUploadTasks.Fallback()
	}

	return l.featureFlags.IntFlag(ctx, featureflags.StorageMaxUploadTasks)
}

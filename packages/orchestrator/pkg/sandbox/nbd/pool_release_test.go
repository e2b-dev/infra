//go:build linux

package nbd

import (
	"context"
	"testing"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unreachableSlot is an index no NBD device can have, so isDeviceFree always
// fails to read /sys/block/nbd<idx>/size and release() keeps returning an
// error. That drives the retry loop deterministically, whether or not the nbd
// module is loaded on the machine running the test.
const unreachableSlot = DeviceSlot(1 << 20)

func retryingPool() *DevicePool {
	return &DevicePool{
		done:      make(chan struct{}),
		usedSlots: bitset.New(16),
		slots:     make(chan DeviceSlot, 1),
	}
}

// A cancelled context must abort the release retry loop while it is backing
// off, not only when it happens to be at the top of the loop. Otherwise every
// stuck device adds a full backoff interval to orchestrator shutdown.
func TestReleaseDeviceAbortsBackoffOnContextCancel(t *testing.T) {
	t.Parallel()

	pool := retryingPool()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel once the first failed attempt has entered the backoff.
	time.AfterFunc(releaseRetryDelay/10, cancel)

	start := time.Now()
	err := pool.ReleaseDevice(ctx, unreachableSlot, WithInfiniteRetry())
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, elapsed, releaseRetryDelay,
		"ReleaseDevice slept through the cancellation instead of aborting the backoff")
}

// The same applies to the deadline installed by WithTimeout, which Close relies
// on to bound how long a single stuck device can hold up the pool.
func TestReleaseDeviceAbortsBackoffOnTimeout(t *testing.T) {
	t.Parallel()

	pool := retryingPool()

	start := time.Now()
	err := pool.ReleaseDevice(t.Context(), unreachableSlot,
		WithInfiniteRetry(),
		WithTimeout(releaseRetryDelay/10),
	)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, releaseRetryDelay,
		"ReleaseDevice slept past its own deadline")
}

// Without infinite retry the first failure is returned immediately, so the
// backoff change must not alter that path.
func TestReleaseDeviceReturnsFirstErrorWithoutInfiniteRetry(t *testing.T) {
	t.Parallel()

	pool := retryingPool()

	err := pool.ReleaseDevice(t.Context(), unreachableSlot)

	require.Error(t, err)
	assert.NotErrorIs(t, err, context.Canceled)
}

//go:build linux

package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// noopProxy satisfies the proxyPool interface without any real networking.
type noopProxy struct{}

func (noopProxy) RemoveFromPool(_ string) error { return nil }

// newTestServer builds the minimal Server needed to exercise setupSandboxLifecycle.
func newTestServer(t *testing.T, semLimit int64) *Server {
	t.Helper()

	sem, err := utils.NewAdjustableSemaphore(semLimit)
	require.NoError(t, err)

	return &Server{
		proxy:             noopProxy{},
		startingSandboxes: sem,
	}
}

// semUsed reads the current "used" count from the semaphore by trying to
// acquire one more slot than the limit allows — if it fails, all slots are
// taken; otherwise we release the probe and count backwards.
// Simpler: just expose via SetLimit trick or use a helper channel.
// We use a direct approach: acquire(limit) succeeds only when used==0.
func semFree(t *testing.T, sem *utils.AdjustableSemaphore, limit int64) int64 {
	t.Helper()
	// Try to acquire all remaining capacity; count how many we can grab.
	var grabbed int64
	for i := int64(0); i < limit; i++ {
		if sem.TryAcquire(1) {
			grabbed++
		} else {
			break
		}
	}
	// Release what we grabbed.
	if grabbed > 0 {
		sem.Release(grabbed)
	}
	return grabbed
}

// TestSemaphoreReleasedAfterSandboxStops verifies that the semaphore slot is
// NOT released when setupSandboxLifecycle is called, but IS released only
// after the sandbox signals exit and cleanup completes.
func TestSemaphoreReleasedAfterSandboxStops(t *testing.T) {
	t.Parallel()

	const limit = 2
	srv := newTestServer(t, limit)

	// Manually acquire one slot (simulating what Create() does).
	acquired := srv.startingSandboxes.TryAcquire(1)
	require.True(t, acquired, "should be able to acquire semaphore slot")

	handle := sandbox.NewTestSandbox("sbx-1")

	// Semaphore has 1 slot used; 1 free.
	assert.Equal(t, int64(1), semFree(t, srv.startingSandboxes, limit))

	// Start lifecycle goroutine — it will block on sbx.Wait().
	srv.setupSandboxLifecycle(context.Background(), handle.Sbx, true)

	// Give the goroutine a moment to start and block on Wait().
	time.Sleep(10 * time.Millisecond)

	// Semaphore slot must still be held while sandbox is running.
	assert.Equal(t, int64(1), semFree(t, srv.startingSandboxes, limit),
		"semaphore must remain acquired while sandbox is still running")

	// Signal sandbox exit — unblocks Wait(), triggers Close(), then Release().
	handle.SignalExit()

	// Wait for the goroutine to finish releasing the semaphore.
	require.Eventually(t, func() bool {
		return semFree(t, srv.startingSandboxes, limit) == limit
	}, 2*time.Second, 5*time.Millisecond,
		"semaphore must be released after sandbox stops")
}

// TestSemaphoreNotReleasedWhenFlagFalse verifies that passing semaphoreAcquired=false
// does not release the semaphore (used when Create() fails before acquiring).
func TestSemaphoreNotReleasedWhenFlagFalse(t *testing.T) {
	t.Parallel()

	const limit = 2
	srv := newTestServer(t, limit)

	// Do NOT acquire the semaphore — simulate an error path where the semaphore
	// was never acquired but setupSandboxLifecycle is called with false.
	handle := sandbox.NewTestSandbox("sbx-noacquire")

	// All slots free initially.
	assert.Equal(t, int64(limit), semFree(t, srv.startingSandboxes, limit))

	srv.setupSandboxLifecycle(context.Background(), handle.Sbx, false)

	// Signal exit immediately.
	handle.SignalExit()

	// Give goroutine time to complete.
	time.Sleep(50 * time.Millisecond)

	// Semaphore must still be fully free — no spurious release.
	assert.Equal(t, int64(limit), semFree(t, srv.startingSandboxes, limit),
		"semaphore must not be released when semaphoreAcquired=false")
}

// TestSemaphoreLimitEnforcedAcrossConcurrentSandboxes verifies that with
// limit=1, a second sandbox cannot start until the first one stops.
func TestSemaphoreLimitEnforcedAcrossConcurrentSandboxes(t *testing.T) {
	t.Parallel()

	const limit = 1
	srv := newTestServer(t, limit)

	// Acquire the only slot (first sandbox starting).
	acquired := srv.startingSandboxes.TryAcquire(1)
	require.True(t, acquired, "first acquire must succeed")

	handle1 := sandbox.NewTestSandbox("sbx-limit-1")
	srv.setupSandboxLifecycle(context.Background(), handle1.Sbx, true)

	// Second sandbox tries to acquire — must fail (limit exhausted).
	secondAcquired := srv.startingSandboxes.TryAcquire(1)
	assert.False(t, secondAcquired, "second acquire must fail while first sandbox is running")

	// Stop the first sandbox.
	handle1.SignalExit()

	// Now the slot should become available.
	require.Eventually(t, func() bool {
		return srv.startingSandboxes.TryAcquire(1)
	}, 2*time.Second, 5*time.Millisecond,
		"semaphore slot must be available after first sandbox stops")

	// Release the probe acquire we just did.
	srv.startingSandboxes.Release(1)
}

// TestSemaphoreReleasedOnExitWithError verifies that the semaphore is still
// released even when the sandbox exits with an error.
func TestSemaphoreReleasedOnExitWithError(t *testing.T) {
	t.Parallel()

	const limit = 1
	srv := newTestServer(t, limit)

	acquired := srv.startingSandboxes.TryAcquire(1)
	require.True(t, acquired)

	handle := sandbox.NewTestSandbox("sbx-err")
	srv.setupSandboxLifecycle(context.Background(), handle.Sbx, true)

	time.Sleep(10 * time.Millisecond)

	// Sandbox exits with an error (e.g., OOM, crash).
	handle.SignalExitWithError(assert.AnError)

	require.Eventually(t, func() bool {
		return semFree(t, srv.startingSandboxes, limit) == limit
	}, 2*time.Second, 5*time.Millisecond,
		"semaphore must be released even when sandbox exits with error")
}

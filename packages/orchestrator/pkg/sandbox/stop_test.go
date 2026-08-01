//go:build linux

package sandbox

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errStopFailed stands in for the ordinary, transient failures doStop can
// return: "fc process %d still exists after SIGKILL", "cgroup %s still has
// processes after cgroup.kill", or a canceled context in the FC-exit wait.
var errStopFailed = errors.New("stop failed")

// TestSandboxStop_RetriesAfterFailure is a regression test for the orphaned
// Firecracker leak.
//
// Stop used to memoize its result in a utils.Lazy[error], which is backed by
// sync.Once and therefore caches the *failure* too. Every later caller — the
// priority cleanup hook, the exit-watcher goroutine, and stopSandboxAsync —
// then got the stale error back without the kill ever being re-attempted, so a
// single transient failure permanently orphaned the VM (and, with it, its
// netns/tap/iptables rules).
//
// The latch must close only on success.
func TestSandboxStop_RetriesAfterFailure(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	var calls atomic.Int32
	stop := func() error {
		if calls.Add(1) == 1 {
			return errStopFailed
		}

		return nil
	}

	err := sbx.latchStop(stop)
	require.ErrorIs(t, err, errStopFailed, "first attempt must surface the stop failure")
	require.EqualValues(t, 1, calls.Load())

	err = sbx.latchStop(stop)
	require.NoError(t, err, "the retry succeeded, so Stop must report success")
	require.EqualValues(t, 2, calls.Load(), "a failed stop must be retried, not served from a cached error")
}

// TestSandboxStop_RetriesEveryFailure checks that the latch stays open across
// repeated failures and that each attempt returns its own error rather than the
// first one.
func TestSandboxStop_RetriesEveryFailure(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	var calls atomic.Int32
	stop := func() error {
		return fmt.Errorf("attempt %d: %w", calls.Add(1), errStopFailed)
	}

	for attempt := 1; attempt <= 3; attempt++ {
		err := sbx.latchStop(stop)
		require.ErrorIs(t, err, errStopFailed)
		assert.EqualValues(t, attempt, calls.Load())
		assert.Contains(t, err.Error(), fmt.Sprintf("attempt %d", attempt), "each attempt must return its own error, not a cached one")
	}
}

// TestSandboxStop_NoRetryAfterSuccess checks the other half of the contract:
// once a stop succeeds the sandbox stays stopped, so repeated Stop calls (the
// cleanup hook plus the exit watcher both fire on a normal teardown) must not
// re-run the kill.
func TestSandboxStop_NoRetryAfterSuccess(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	var calls atomic.Int32
	stop := func() error {
		calls.Add(1)

		return nil
	}

	for range 3 {
		require.NoError(t, sbx.latchStop(stop))
	}

	require.EqualValues(t, 1, calls.Load(), "a successful stop must latch")
}

// TestSandboxStop_RetryLatchesAfterSuccess combines both halves: a failure is
// retried, and the successful retry latches.
func TestSandboxStop_RetryLatchesAfterSuccess(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	var calls atomic.Int32
	stop := func() error {
		if calls.Add(1) == 1 {
			return errStopFailed
		}

		return nil
	}

	require.ErrorIs(t, sbx.latchStop(stop), errStopFailed)
	require.NoError(t, sbx.latchStop(stop))
	require.NoError(t, sbx.latchStop(stop))
	require.EqualValues(t, 2, calls.Load(), "the successful retry must latch")
}

// TestSandboxStop_ConcurrentCallersRunStopOnce checks that concurrent callers
// are serialized and, on success, the work runs exactly once. Run under -race.
func TestSandboxStop_ConcurrentCallersRunStopOnce(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	var (
		calls    atomic.Int32
		inFlight atomic.Int32
	)
	stop := func() error {
		assert.EqualValues(t, 1, inFlight.Add(1), "stop attempts must not overlap")
		defer inFlight.Add(-1)

		calls.Add(1)

		return nil
	}

	const callers = 16

	var wg sync.WaitGroup

	start := make(chan struct{})

	for range callers {
		wg.Go(func() {
			<-start

			assert.NoError(t, sbx.latchStop(stop))
		})
	}

	close(start)
	wg.Wait()

	require.EqualValues(t, 1, calls.Load(), "concurrent callers must run the stop work once")
}

// TestSandboxStop_ConcurrentCallersRetryUntilSuccess checks that a failing stop
// under concurrency is retried by the callers that follow, and stops being
// retried once one of them succeeds.
func TestSandboxStop_ConcurrentCallersRetryUntilSuccess(t *testing.T) {
	t.Parallel()

	sbx := &Sandbox{}

	const failures = 4

	var (
		calls    atomic.Int32
		inFlight atomic.Int32
	)
	stop := func() error {
		assert.EqualValues(t, 1, inFlight.Add(1), "stop attempts must not overlap")
		defer inFlight.Add(-1)

		if calls.Add(1) <= failures {
			return errStopFailed
		}

		return nil
	}

	const callers = 16

	var (
		wg        sync.WaitGroup
		successes atomic.Int32
	)

	start := make(chan struct{})

	for range callers {
		wg.Go(func() {
			<-start

			if sbx.latchStop(stop) == nil {
				successes.Add(1)
			}
		})
	}

	close(start)
	wg.Wait()

	// The first `failures` callers each retry; the next one succeeds and latches,
	// so every remaining caller is a no-op that also reports success.
	require.EqualValues(t, failures+1, calls.Load(), "the stop must be retried until it succeeds, then latch")
	require.EqualValues(t, callers-failures, successes.Load())
}

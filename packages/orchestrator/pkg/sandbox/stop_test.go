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

// errStopFailed stands in for the transient failures doStop can return.
var errStopFailed = errors.New("stop failed")

// Regression test: Stop used to memoize its result in a utils.Lazy[error]
// (sync.Once-backed), so a single transient failure was cached forever and the
// Firecracker VM leaked. A failed stop must stay retryable.
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

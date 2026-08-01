//go:build linux

package network

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStorage struct {
	released  atomic.Int64
	releaseFn func(*Slot) error

	// ctxErrMu guards releaseCtxErrs, which records ctx.Err() as seen at the
	// start of each Release call. A non-nil entry means the caller handed the
	// storage a dead context, which a real backend would refuse to act on.
	ctxErrMu       sync.Mutex
	releaseCtxErrs []error
}

func (f *fakeStorage) Acquire(_ context.Context) (*Slot, error) {
	return nil, context.Canceled
}

func (f *fakeStorage) Release(ctx context.Context, s *Slot) error {
	f.released.Add(1)

	f.ctxErrMu.Lock()
	f.releaseCtxErrs = append(f.releaseCtxErrs, ctx.Err())
	f.ctxErrMu.Unlock()

	if f.releaseFn != nil {
		return f.releaseFn(s)
	}

	return nil
}

func (f *fakeStorage) releaseCtxErrors() []error {
	f.ctxErrMu.Lock()
	defer f.ctxErrMu.Unlock()

	return append([]error(nil), f.releaseCtxErrs...)
}

// testSlotIdxOffset keeps test slot indices outside the range any real
// pool would allocate (vrtSlotsSize == 32766) so cleanup()'s namespace
// teardown can't collide with namespaces another test binary is actively
// using — e.g. the smoketest package creating ns-2, ns-3, …
const testSlotIdxOffset = 1 << 30

func newTestSlot(idx int) *Slot {
	return &Slot{Idx: idx + testSlotIdxOffset, egressProxy: NoopEgressProxy{}}
}

// noopRelease satisfies returnSlot's ReleaseNotify parameter without doing
// anything. Tests cover the cleanup path and don't care about the
// network-release notification.
func noopRelease(context.Context, string) {}

// TestReturn_NoPanicDuringClose races Return against Close to guard
// against regressions of the send-on-closed-channel panic.
func TestReturn_NoPanicDuringClose(t *testing.T) {
	t.Parallel()

	const runs = 20
	const workers = 32
	const iters = 50

	for run := range runs {
		storage := &fakeStorage{}
		pool := NewPool(2, workers*iters, storage, Config{})
		close(pool.newSlots)

		var wg sync.WaitGroup
		start := make(chan struct{})

		for w := range workers {
			wg.Go(func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("Return panicked (run=%d worker=%d): %v", run, w, r)
					}
				}()

				<-start

				for i := range iters {
					_ = pool.returnSlot(t.Context(), newTestSlot(w*iters+i+1), noopRelease, 0)
				}
			})
		}

		close(start)
		_ = pool.Close(t.Context())

		wg.Wait()
	}
}

func TestClose_Idempotent(t *testing.T) {
	t.Parallel()

	pool := NewPool(2, 4, &fakeStorage{}, Config{})
	close(pool.newSlots)

	require.NoError(t, pool.Close(t.Context()))
	require.NoError(t, pool.Close(t.Context()))
}

func TestReturn_AfterCloseCleansUpLocally(t *testing.T) {
	t.Parallel()

	storage := &fakeStorage{}
	pool := NewPool(2, 4, storage, Config{})
	close(pool.newSlots)

	require.NoError(t, pool.Close(t.Context()))

	before := storage.released.Load()
	err := pool.returnSlot(t.Context(), newTestSlot(1), noopRelease, 0)
	after := storage.released.Load()

	assert.Equal(t, int64(1), after-before, "Return after Close must invoke Storage.Release via cleanup")
	require.ErrorIs(t, err, ErrClosed)
}

func TestReturnAsync_DoesNotBlockOnReturnDelay(t *testing.T) {
	t.Parallel()

	storage := &fakeStorage{}
	pool := NewPool(2, 4, storage, Config{})
	close(pool.newSlots)

	done := make(chan struct{})
	go func() {
		defer close(done)

		_ = pool.ReturnAsync(t.Context(), newTestSlot(1), noopRelease, time.Hour)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ReturnAsync blocked on the return delay")
	}

	// Close short-circuits the hour-long delay into the cleanup path and
	// must not return before the slot is released.
	require.NoError(t, pool.Close(t.Context()))
	assert.Equal(t, int64(1), storage.released.Load(), "Close returned before the in-flight return released the slot")
}

// Every slot handed to ReturnAsync must be released by the time Close
// returns, whether cleaned up in flight or drained from reusedSlots.
func TestClose_WaitsForInFlightAsyncReturns(t *testing.T) {
	t.Parallel()

	const slots = 8

	storage := &fakeStorage{}
	pool := NewPool(2, slots, storage, Config{})
	close(pool.newSlots)

	for i := range slots {
		require.NoError(t, pool.ReturnAsync(t.Context(), newTestSlot(i+1), noopRelease, 10*time.Millisecond))
	}

	// Close error ignored: cleanup()'s netlink/iptables teardown may fail
	// in the test environment; the release count is the leak signal.
	_ = pool.Close(t.Context())

	assert.Equal(t, int64(slots), storage.released.Load(), "Close returned before all in-flight returns released their slots")
}

// After Close, ReturnAsync cannot register with Close's wait, so it must
// clean the slot up inline before returning.
func TestReturnAsync_AfterCloseCleansUpSynchronously(t *testing.T) {
	t.Parallel()

	storage := &fakeStorage{}
	pool := NewPool(2, 4, storage, Config{})
	close(pool.newSlots)

	require.NoError(t, pool.Close(t.Context()))

	err := pool.ReturnAsync(t.Context(), newTestSlot(1), noopRelease, time.Hour)
	require.ErrorIs(t, err, ErrClosed)
	assert.Equal(t, int64(1), storage.released.Load(), "ReturnAsync after Close must release the slot before returning")
}

// TestClose_ReleasesPooledSlotsWithCanceledContext guards the shutdown path
// the template-manager actually runs: it sets FORCE_STOP, which cancels the
// close context before any closer runs. Close must still hand Storage.Release
// a live context — the storage key is node-scoped and nothing reclaims it, so
// releases skipped here leak those slot indices for the lifetime of the node.
func TestClose_ReleasesPooledSlotsWithCanceledContext(t *testing.T) {
	t.Parallel()

	const slots = 4

	storage := &fakeStorage{}
	pool := NewPool(2, slots, storage, Config{})
	close(pool.newSlots)

	for i := range slots {
		pool.reusedSlots <- newTestSlot(i + 1)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Close error ignored: cleanup()'s netlink teardown may fail in the test
	// environment; the release accounting is the leak signal.
	_ = pool.Close(ctx)

	require.Equal(t, int64(slots), storage.released.Load(),
		"Close must release every pooled slot even when its context is already canceled")

	for i, err := range storage.releaseCtxErrors() {
		assert.NoErrorf(t, err, "Release call %d received a dead context, so the slot's storage key would leak", i)
	}
}

func TestReturn_AfterClose_CleanupFailure_PreservesErrClosed(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	storage := &fakeStorage{releaseFn: func(_ *Slot) error { return boom }}
	pool := NewPool(2, 4, storage, Config{})
	close(pool.newSlots)

	require.NoError(t, pool.Close(t.Context()))

	err := pool.returnSlot(t.Context(), newTestSlot(1), noopRelease, 0)
	require.ErrorIs(t, err, ErrClosed)
	require.ErrorIs(t, err, boom)
}

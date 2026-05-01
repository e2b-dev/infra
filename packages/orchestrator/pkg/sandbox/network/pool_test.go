package network

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStorage struct {
	released  atomic.Int64
	releaseFn func(*Slot) error
}

func (f *fakeStorage) Acquire(_ context.Context) (*Slot, error) {
	return nil, context.Canceled
}

func (f *fakeStorage) Release(s *Slot) error {
	f.released.Add(1)
	if f.releaseFn != nil {
		return f.releaseFn(s)
	}

	return nil
}

// testSlotIdxOffset keeps test slot indices outside the range any real
// pool would allocate (vrtSlotsSize == 32766) so cleanup()'s namespace
// teardown can't collide with namespaces another test binary is actively
// using — e.g. the smoketest package creating ns-2, ns-3, …
const testSlotIdxOffset = 1 << 30

func newTestSlot(idx int) *Slot {
	return &Slot{Idx: idx + testSlotIdxOffset, egressProxy: NoopEgressProxy{}}
}

// noopRelease satisfies Pool.Return's ReleaseNotify parameter without doing
// anything. Tests cover Return's cleanup path and don't care about the
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
					_ = pool.Return(t.Context(), newTestSlot(w*iters+i+1), noopRelease, 0)
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
	err := pool.Return(t.Context(), newTestSlot(1), noopRelease, 0)
	after := storage.released.Load()

	assert.Equal(t, int64(1), after-before, "Return after Close must invoke Storage.Release via cleanup")
	require.ErrorIs(t, err, ErrClosed)
}

func TestReturn_AfterClose_CleanupFailure_PreservesErrClosed(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	storage := &fakeStorage{releaseFn: func(_ *Slot) error { return boom }}
	pool := NewPool(2, 4, storage, Config{})
	close(pool.newSlots)

	require.NoError(t, pool.Close(t.Context()))

	err := pool.Return(t.Context(), newTestSlot(1), noopRelease, 0)
	require.ErrorIs(t, err, ErrClosed)
	require.ErrorIs(t, err, boom)
}

//go:build linux

package v2

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

type failingStorage struct {
	attempts atomic.Int64
	released atomic.Int64
	// failures > 0 parks every attempt after that many until its context ends.
	failures int64
	mu       sync.Mutex
	at       []time.Time
}

func (s *failingStorage) Acquire(ctx context.Context) (*network.Slot, error) {
	n := s.attempts.Add(1)
	s.mu.Lock()
	s.at = append(s.at, time.Now())
	s.mu.Unlock()
	if s.failures > 0 && n > s.failures {
		<-ctx.Done()

		return nil, ctx.Err()
	}

	return nil, errors.New("persistent acquire failure")
}

func (s *failingStorage) Release(*network.Slot) error {
	s.released.Add(1)

	return nil
}

func newRetryTestPool(t *testing.T, storage network.Storage, delay time.Duration) (*V2Pool, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	metrics, err := NewMetrics(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	require.NoError(t, err)

	return NewV2Pool(storage, testConfig(), nil, nil, WithPoolMetrics(metrics), WithPopulateRetryDelay(delay)), reader
}

func TestV2Pool_PopulateBacksOffAfterCreationFailure(t *testing.T) {
	t.Parallel()

	const delay = 50 * time.Millisecond
	storage := &failingStorage{failures: 3}
	pool, reader := newRetryTestPool(t, storage, delay)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		pool.Populate(ctx)
		close(done)
	}()

	// Populate reaches the parked attempt only after it has handled every failure.
	require.Eventually(t, func() bool { return storage.attempts.Load() == storage.failures+1 }, 5*time.Second, time.Millisecond)
	cancel()
	requireReceive(t, done)

	for i := 1; i < len(storage.at); i++ {
		require.GreaterOrEqual(t, storage.at[i].Sub(storage.at[i-1]), delay, "attempt %d did not wait out the retry delay", i+1)
	}

	values := collectCounterValues(t, reader)
	require.Equal(t, storage.failures, values[counterKey(metricCreationFailures, "stage", string(slotCreationStageAcquire))])
}

func TestV2Pool_PopulateCancellationExitsBackoffPromptly(t *testing.T) {
	t.Parallel()

	storage := &failingStorage{}
	pool, _ := newRetryTestPool(t, storage, time.Hour)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { pool.Populate(ctx); close(done) }()
	require.Eventually(t, func() bool { return storage.attempts.Load() == 1 }, time.Second, time.Millisecond)
	cancel()
	requireReceive(t, done)
}

func TestV2Pool_CloseExitsBackoffPromptly(t *testing.T) {
	t.Parallel()

	storage := &failingStorage{}
	pool, _ := newRetryTestPool(t, storage, time.Hour)
	done := make(chan struct{})
	go func() { pool.Populate(t.Context()); close(done) }()
	require.Eventually(t, func() bool { return storage.attempts.Load() == 1 }, time.Second, time.Millisecond)
	require.NoError(t, pool.Close(t.Context()))
	requireReceive(t, done)
}

func requireReceive(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Populate did not exit promptly")
	}
}

// A failed release notification keeps the slot allocated and out of reuse on
// the normal, canceled and closed paths. An async return still finishes, so
// Close does not wait on it.
func TestV2Pool_FailedReleaseRetainsSlot(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	failRelease := func(context.Context, string) error { return boom }
	storage := &failingStorage{}
	pool, _ := newRetryTestPool(t, storage, time.Hour)
	close(pool.newSlots)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, pool.returnSlot(t.Context(), &network.Slot{Idx: 1}, failRelease, 0), boom)
	err := pool.returnSlot(canceled, &network.Slot{Idx: 2}, failRelease, time.Hour)
	require.ErrorIs(t, err, network.ErrSlotRetained)
	require.ErrorIs(t, err, context.Canceled)
	require.NoError(t, pool.ReturnAsync(t.Context(), &network.Slot{Idx: 3}, failRelease, time.Hour))
	require.NoError(t, pool.Close(t.Context()))
	err = pool.returnSlot(t.Context(), &network.Slot{Idx: 4}, failRelease, time.Hour)
	require.ErrorIs(t, err, network.ErrSlotRetained)
	require.ErrorIs(t, err, network.ErrClosed)

	require.Zero(t, storage.released.Load(), "a retained slot must not be released for reuse")
	require.Empty(t, pool.reusedSlots)
}

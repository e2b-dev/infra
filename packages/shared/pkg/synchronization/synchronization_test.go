package synchronization

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type testStore struct {
	mu sync.Mutex

	source []string
	pool   map[string]string

	inserts int
	removes int
}

func newTestStore(source []string, preExistingPool []string) *testStore {
	pool := make(map[string]string, len(preExistingPool))
	for _, k := range preExistingPool {
		pool[k] = k
	}

	return &testStore{source: source, pool: pool}
}

func (s *testStore) SourceList(context.Context) ([]string, error) {
	return append([]string(nil), s.source...), nil
}

func (s *testStore) SourceExists(_ context.Context, source []string, p string) bool {
	return slices.Contains(source, p)
}

func (s *testStore) PoolList(context.Context) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]string, 0)
	for k := range s.pool {
		out = append(out, k)
	}

	return out
}

func (s *testStore) PoolExists(_ context.Context, item string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pool[item]

	return ok
}

func (s *testStore) PoolInsert(_ context.Context, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pool[value] = value
	s.inserts++
}

func (s *testStore) PoolUpdate(context.Context, string) { /* not used */ }

func (s *testStore) PoolRemove(_ context.Context, item string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, v := range s.pool {
		if v == item {
			delete(s.pool, k)

			break
		}
	}

	s.removes++
}

func newSynchronizer(ctx context.Context, store Store[string, string]) *Synchronize[string, string] {
	logger.ReplaceGlobals(ctx, logger.NewNopLogger())

	return &Synchronize[string, string]{
		store:            store,
		tracerSpanPrefix: "test synchronization",
		logsPrefix:       "test synchronization",
		syncSem:          semaphore.NewWeighted(1),
	}
}

// slowTestStore embeds testStore but overrides SourceList to block until
// the unblock channel is closed. This simulates a long-running sync holding
// the semaphore.
type slowTestStore struct {
	*testStore

	unblock chan struct{}
}

func (s *slowTestStore) SourceList(ctx context.Context) ([]string, error) {
	select {
	case <-s.unblock:
		return s.testStore.SourceList(ctx)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestSynchronize_SyncRespectsContextCancellation verifies that a second
// Sync call returns promptly when its context expires while the first Sync
// holds the semaphore. With the old sync.Mutex this would block indefinitely;
// the semaphore.Weighted implementation respects context cancellation.
func TestSynchronize_SyncRespectsContextCancellation(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	slow := &slowTestStore{
		testStore: newTestStore([]string{"a"}, nil),
		unblock:   make(chan struct{}),
	}
	syncer := newSynchronizer(ctx, slow)

	// First sync: acquires the semaphore and blocks inside SourceList.
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- syncer.Sync(ctx)
	}()

	// Give the first goroutine time to acquire the semaphore.
	time.Sleep(20 * time.Millisecond)

	// Second sync: should fail fast when its context deadline expires.
	shortCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	err := syncer.Sync(shortCtx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Unblock the first sync and verify it completes successfully.
	close(slow.unblock)
	require.NoError(t, <-firstDone)
}

func TestSynchronize_InsertAndRemove(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	// Start with empty pool; source has a & b.
	s := newTestStore([]string{"a", "b"}, nil)
	syncer := newSynchronizer(ctx, s)

	require.NoError(t, syncer.Sync(ctx))
	assert.Equal(t, 2, s.inserts)
	assert.Len(t, s.pool, 2)

	// Now remove "b" from the source – should trigger exactly one removal.
	s.source = []string{"a"}
	require.NoError(t, syncer.Sync(ctx))

	assert.Equal(t, 1, s.removes)
	assert.Len(t, s.pool, 1)
	assert.True(t, s.PoolExists(ctx, "a"))
}

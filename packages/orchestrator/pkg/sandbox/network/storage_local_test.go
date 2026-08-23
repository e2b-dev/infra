//go:build linux

package network

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestStorageLocal builds a StorageLocal over a temp netns dir, snapshotting
// any pre-created entries as foreign the same way NewStorageLocal does.
func newTestStorageLocal(t *testing.T, slotsSize int, existing ...string) *StorageLocal {
	t.Helper()

	dir := t.TempDir()
	for _, name := range existing {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}

	foreign, err := getForeignNamespaces(dir)
	require.NoError(t, err)

	foreignMap := make(map[string]struct{}, len(foreign))
	for _, ns := range foreign {
		foreignMap[ns] = struct{}{}
	}

	return &StorageLocal{
		slotsSize:   slotsSize,
		netnsDir:    dir,
		foreignNs:   foreignMap,
		leakedNs:    map[string]struct{}{},
		acquiredNs:  map[string]struct{}{},
		egressProxy: NoopEgressProxy{},
	}
}

func TestSlotIndexFromNamespace(t *testing.T) {
	t.Parallel()

	idx, ok := SlotIndexFromNamespace("ns-2")
	require.True(t, ok)
	require.Equal(t, 2, idx)

	for _, name := range []string{"host", "ns-0", "ns-nope", "other-2", getSlotName(vrtSlotsSize)} {
		_, ok := SlotIndexFromNamespace(name)
		require.False(t, ok)
	}
}

func TestNewSlotRejectsReservedAndOutOfRangeIndices(t *testing.T) {
	t.Parallel()

	for _, idx := range []int{-1, 0, vrtSlotsSize} {
		_, err := NewSlot("invalid", idx, Config{}, NewNoopEgressProxy())
		require.ErrorContains(t, err, "out of range")
	}

	slot, err := NewSlot("first", 1, Config{}, NewNoopEgressProxy())
	require.NoError(t, err)
	require.Equal(t, 1, slot.Idx)
}

func TestStorageLocalStartsAtOneSkipsLiveNamespacesAndExhausts(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ns-1"), []byte("live"), 0o600))

	storage, err := newStorageLocal(context.Background(), Config{}, NewNoopEgressProxy(), dir)
	require.NoError(t, err)
	storage.slotsSize = 4

	first, err := storage.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, first.Idx)

	// A namespace created after construction is still observed before the
	// reservation is returned; the startup snapshot is not the only guard.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ns-3"), []byte("live"), 0o600))
	_, err = storage.Acquire(context.Background())
	require.ErrorContains(t, err, "no empty slots found")
}

func TestStorageLocalConcurrentAcquireIsUnique(t *testing.T) {
	t.Parallel()

	storage, err := newStorageLocal(context.Background(), Config{}, NewNoopEgressProxy(), t.TempDir())
	require.NoError(t, err)
	storage.slotsSize = 9

	const acquisitions = 8
	indices := make(chan int, acquisitions)
	errs := make(chan error, acquisitions)
	var wg sync.WaitGroup
	for range acquisitions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot, acquireErr := storage.Acquire(context.Background())
			if acquireErr != nil {
				errs <- acquireErr
				return
			}
			indices <- slot.Idx
		}()
	}
	wg.Wait()
	close(indices)
	close(errs)
	for acquireErr := range errs {
		require.NoError(t, acquireErr)
	}

	seen := map[int]bool{}
	for idx := range indices {
		require.GreaterOrEqual(t, idx, 1)
		require.Less(t, idx, storage.slotsSize)
		require.False(t, seen[idx], "slot %d was allocated twice", idx)
		seen[idx] = true
	}
	require.Len(t, seen, acquisitions)

	_, err = storage.Acquire(context.Background())
	require.ErrorContains(t, err, "no empty slots found")
}

func TestStorageLocalConstructionFailureDoesNotLeakReservation(t *testing.T) {
	t.Parallel()

	storage, err := newStorageLocal(context.Background(), Config{}, NewNoopEgressProxy(), t.TempDir())
	require.NoError(t, err)
	storage.slotsSize = 2
	storage.slotFactory = func(string, int) (*Slot, error) {
		return nil, errors.New("slot rejected")
	}

	_, err = storage.Acquire(context.Background())
	require.ErrorContains(t, err, "slot rejected")
	require.Empty(t, storage.acquiredNs)

	storage.slotFactory = func(key string, idx int) (*Slot, error) {
		return &Slot{Key: key, Idx: idx}, nil
	}
	slot, err := storage.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, slot.Idx)
}

func TestListSlotNamespaces(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"ns-10", "ns-2", "host", "ns-bad"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "ns-3"), 0o700))

	indices, err := ListSlotNamespaces(dir)
	require.NoError(t, err)
	require.Equal(t, []int{2, 10}, indices)
}

func TestStorageLocal_AcquireSequential(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 3)

	first, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, first.Idx)

	second, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, second.Idx)
}

func TestStorageLocal_AcquireSkipsForeignSnapshot(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 3, "ns-1")

	slot, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, slot.Idx)
}

func TestStorageLocal_ReleaseReusesIndex(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 3)

	first, err := s.Acquire(t.Context())
	require.NoError(t, err)
	_, err = s.Acquire(t.Context())
	require.NoError(t, err)

	require.NoError(t, s.Release(first))

	again, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, first.Idx, again.Idx)
}

// A leftover namespace (a failed RemoveNetwork keeps it as the reclaim
// anchor) blocks its index only while it exists: Release frees the index,
// scans skip it via the availability check, and it is handed out again once
// the leftover is cleaned up — no orchestrator restart needed.
func TestStorageLocal_LeftoverNamespaceBlocksIndexUntilRemoved(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 3)

	slot, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, slot.Idx)

	// Simulate a created namespace whose teardown failed.
	nsPath := filepath.Join(s.netnsDir, getSlotName(slot.Idx))
	require.NoError(t, os.WriteFile(nsPath, nil, 0o600))
	require.NoError(t, s.Release(slot))

	next, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, next.Idx, "index with a leftover namespace must be skipped")
	require.Empty(t, s.foreignNs, "leftovers must not poison the foreign snapshot")

	require.NoError(t, os.Remove(nsPath))

	again, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, slot.Idx, again.Idx, "index must be reusable once the leftover is gone")
}

func TestStorageLocal_UntrackedNamespaceSkippedWithoutPoisoning(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 3)

	// Appears after the construction-time snapshot without being acquired.
	require.NoError(t, os.WriteFile(filepath.Join(s.netnsDir, "ns-1"), nil, 0o600))

	slot, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, slot.Idx)
	require.Empty(t, s.foreignNs)

	require.NoError(t, os.Remove(filepath.Join(s.netnsDir, "ns-1")))

	again, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, again.Idx)
}

// A NewSlot failure must not consume the index: the walk reaching an index
// outside NewSlot's valid range surfaces the error and leaves acquiredNs
// untouched.
//
// Deliberately not parallel: the walk skips vrtSlotsSize (~32k) blocked
// indexes inside Acquire's 500ms budget, which is milliseconds uncontended
// but can exceed the budget when competing with the package's parallel
// tests on a loaded CI runner.
func TestStorageLocal_FailedNewSlotIsNotAllocated(t *testing.T) { //nolint:paralleltest // intentionally serialized to stay within Acquire's deadline on loaded runners
	// Block every valid index so the walk reaches vrtSlotsSize+1, which
	// NewSlot rejects.
	foreign := make(map[string]struct{}, vrtSlotsSize)
	for idx := 1; idx <= vrtSlotsSize; idx++ {
		foreign[getSlotName(idx)] = struct{}{}
	}

	s := &StorageLocal{
		slotsSize:   vrtSlotsSize + 1,
		netnsDir:    t.TempDir(),
		foreignNs:   foreign,
		leakedNs:    map[string]struct{}{},
		acquiredNs:  map[string]struct{}{},
		egressProxy: NoopEgressProxy{},
	}

	_, err := s.Acquire(t.Context())
	require.ErrorContains(t, err, "out of range")
	require.Empty(t, s.acquiredNs, "failed NewSlot must not leave the index allocated")
}

// A transient RemoveNetwork failure leaves the namespace as the reclaim
// anchor while Release frees the index. Once no clean index remains, Acquire
// must hand the leaked index out again (CreateNetwork then finishes the
// teardown) instead of reporting exhaustion — repeated transient failures
// must not drain the node until restart.
func TestStorageLocal_ExhaustionFallsBackToLeakedIndex(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 2)

	first, err := s.Acquire(t.Context())
	require.NoError(t, err)
	_, err = s.Acquire(t.Context())
	require.NoError(t, err)

	// Failed teardown: the namespace outlives the release.
	nsPath := filepath.Join(s.netnsDir, getSlotName(first.Idx))
	require.NoError(t, os.WriteFile(nsPath, nil, 0o600))
	require.NoError(t, s.Release(first))

	reclaimed, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, first.Idx, reclaimed.Idx)

	// A failed reclaim releases the slot again; the retry must repeat.
	require.NoError(t, s.Release(reclaimed))
	again, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, first.Idx, again.Idx)
}

// One leaked index whose teardown keeps failing must not monopolize the
// exhaustion fallback: the randomized selection eventually attempts every
// leaked index.
func TestStorageLocal_ExhaustionRotatesLeakedIndexes(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 2)

	first, err := s.Acquire(t.Context())
	require.NoError(t, err)
	second, err := s.Acquire(t.Context())
	require.NoError(t, err)

	for _, slot := range []*Slot{first, second} {
		require.NoError(t, os.WriteFile(filepath.Join(s.netnsDir, getSlotName(slot.Idx)), nil, 0o600))
		require.NoError(t, s.Release(slot))
	}

	seen := map[int]bool{}
	for range 100 {
		slot, err := s.Acquire(t.Context())
		require.NoError(t, err)
		seen[slot.Idx] = true
		require.NoError(t, s.Release(slot))

		if len(seen) == 2 {
			break
		}
	}

	require.Len(t, seen, 2, "both leaked indexes must eventually be attempted")
}

// A transient error while checking the namespace at release time must not
// strand the index: it is conservatively recorded as leaked (still
// reclaimable), and the stale record self-cleans once the index is handed
// out again.
func TestStorageLocal_ReleaseCheckErrorKeepsIndexReclaimable(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 1)

	slot, err := s.Acquire(t.Context())
	require.NoError(t, err)

	// Self-referencing symlink: os.Stat fails with ELOOP, not IsNotExist.
	nsPath := filepath.Join(s.netnsDir, getSlotName(slot.Idx))
	require.NoError(t, os.Symlink(getSlotName(slot.Idx), nsPath))

	require.Error(t, s.Release(slot))
	require.Empty(t, s.acquiredNs)
	require.Contains(t, s.leakedNs, getSlotName(slot.Idx))

	// Namespace gone: the next scan hands the index out and clears the record.
	require.NoError(t, os.Remove(nsPath))

	again, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, slot.Idx, again.Idx)
	require.Empty(t, s.leakedNs)
}

// Namespaces this storage never released stay untouched even at exhaustion:
// only leaks recorded by Release are reclaim candidates.
func TestStorageLocal_ExhaustionSkipsExternalNamespace(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 2)

	require.NoError(t, os.WriteFile(filepath.Join(s.netnsDir, "ns-1"), nil, 0o600))

	slot, err := s.Acquire(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, slot.Idx)

	_, err = s.Acquire(t.Context())
	require.ErrorContains(t, err, "no empty slots found")
}

func TestStorageLocal_AcquireExhaustion(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 2)

	for want := 1; want <= 2; want++ {
		slot, err := s.Acquire(t.Context())
		require.NoError(t, err)
		require.Equal(t, want, slot.Idx)
	}

	_, err := s.Acquire(t.Context())
	require.ErrorContains(t, err, "no empty slots found")
	require.Len(t, s.acquiredNs, 2, "failed acquire must not leave phantom entries")
}

func TestStorageLocal_ConcurrentAcquireRelease(t *testing.T) {
	t.Parallel()

	s := newTestStorageLocal(t, 64)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 16 {
				slot, err := s.Acquire(t.Context())
				if err != nil {
					continue
				}
				require.NoError(t, s.Release(slot))
			}
		})
	}
	wg.Wait()

	require.Empty(t, s.acquiredNs)
}

func TestGetForeignNamespaces(t *testing.T) {
	t.Parallel()

	ns, err := getForeignNamespaces(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	require.Empty(t, ns)

	dir := t.TempDir()
	for _, name := range []string{"host", "ns-3", "cni-abc"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}

	ns, err = getForeignNamespaces(dir)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ns-3", "cni-abc"}, ns)
}

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

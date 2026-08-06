//go:build linux

package network

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStorageMemoryConstructionFailureDoesNotLeakReservation(t *testing.T) {
	storage, err := NewStorageMemory(2, Config{}, NewNoopEgressProxy())
	require.NoError(t, err)
	storage.slotFactory = func(string, int) (*Slot, error) {
		return nil, errors.New("slot rejected")
	}

	_, err = storage.Acquire(context.Background())
	require.ErrorContains(t, err, "slot rejected")
	require.False(t, storage.freeSlots[1])

	storage.slotFactory = func(key string, idx int) (*Slot, error) {
		return &Slot{Key: key, Idx: idx}, nil
	}
	slot, err := storage.Acquire(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, slot.Idx)
}

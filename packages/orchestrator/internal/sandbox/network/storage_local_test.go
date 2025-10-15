package network

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageLocalRoundTrip(t *testing.T) {
	t.Setenv("LOCAL_NAMESPACE_STORAGE_DIR", t.TempDir())

	config, err := ParseConfig()
	require.NoError(t, err)

	instance, err := NewStorageLocal(2, config)
	require.NoError(t, err)

	slot1, err := instance.Acquire(t.Context())
	require.NoError(t, err)
	assert.Positive(t, slot1.Idx)

	slot2, err := instance.Acquire(t.Context())
	require.NoError(t, err)
	assert.Positive(t, slot2.Idx)

	err = instance.Release(t.Context(), slot1)
	require.NoError(t, err)

	slot1, err = instance.Acquire(t.Context())
	require.NoError(t, err)
	assert.Positive(t, slot1.Idx)

	slot3, err := instance.Acquire(t.Context())
	assert.Nil(t, slot3)
	require.ErrorIs(t, err, ErrNoSlotFound)
}

func TestStorageLocal_LockingCreatesDirsWhenAppropriate(t *testing.T) {
	config := Config{
		LocalNamespaceStorageDir: filepath.Join(t.TempDir(), "missing-dir"),
	}

	instance, err := NewStorageLocal(2, config)
	require.NoError(t, err)

	err = instance.lockSlot(1)
	require.NoError(t, err)
}

func TestStorageLocal_LockingAndUnlocking(t *testing.T) {
	config := Config{
		LocalNamespaceStorageDir: t.TempDir(),
	}

	instance, err := NewStorageLocal(2, config)
	require.NoError(t, err)

	// lock the first slot
	err = instance.lockSlot(1)
	require.NoError(t, err)

	// lock a different slot
	err = instance.lockSlot(2)
	require.NoError(t, err)

	// try to lock the first slot again, should fail
	err = instance.lockSlot(1)
	require.ErrorIs(t, err, os.ErrExist)

	// unlock the first slot
	instance.unlockSlot(1)

	// unlock the first slot again, should not panic
	instance.unlockSlot(1)

	// try to lock the first slot again, should succeed
	err = instance.lockSlot(1)
	require.NoError(t, err)

	// try to lock the second slot again, should fail
	err = instance.lockSlot(2)
	require.ErrorIs(t, err, os.ErrExist)
	instance.unlockSlot(2)
}

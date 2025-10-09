package network

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStorageLocalRoundTrip(t *testing.T) {
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

	err = instance.Release(slot1)
	require.NoError(t, err)

	slot1, err = instance.Acquire(t.Context())
	require.NoError(t, err)
	assert.Positive(t, slot1.Idx)

	slot3, err := instance.Acquire(t.Context())
	assert.Nil(t, slot3)
	require.ErrorIs(t, err, ErrNetworkSlotsExhausted)
}

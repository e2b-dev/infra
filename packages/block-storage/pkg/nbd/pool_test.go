package nbd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNbdDevicePool(t *testing.T) {
	pool, err := NewDevicePool()
	require.NoError(t, err)

	nbd0, err := pool.GetDevice()
	defer func() {
		require.NoError(t, pool.ReleaseDevice(nbd0))
	}()

	require.NoError(t, err)

	require.Equal(t, nbd0, "/dev/nbd0")

	nbd1, err := pool.GetDevice()
	defer func() {
		require.NoError(t, pool.ReleaseDevice(nbd1))
	}()

	require.NoError(t, err)

	require.Equal(t, nbd1, "/dev/nbd1")
}

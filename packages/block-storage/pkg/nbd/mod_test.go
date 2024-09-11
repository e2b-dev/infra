package nbd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNbdDevicePool(t *testing.T) {
	pool, err := NewNbdDevicePool()
	require.NoError(t, err)

	nbd0, err := pool.GetDevice()
	defer pool.ReleaseDevice(nbd0)

	require.NoError(t, err)

	require.Equal(t, nbd0, "/dev/nbd0")

	nbd1, err := pool.GetDevice()
	defer pool.ReleaseDevice(nbd1)

	require.NoError(t, err)

	require.Equal(t, nbd1, "/dev/nbd1")
}

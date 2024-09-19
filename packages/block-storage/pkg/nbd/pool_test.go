package nbd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNbdDevicePool(t *testing.T) {
	pool, err := NewNbdDevicePool()
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nbd0, err := pool.GetDevice(ctx)
	defer pool.ReleaseDevice(nbd0)

	require.NoError(t, err)

	require.Equal(t, nbd0, "/dev/nbd0")

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	nbd1, err := pool.GetDevice(ctx)
	defer pool.ReleaseDevice(nbd1)

	require.NoError(t, err)

	require.Equal(t, nbd1, "/dev/nbd1")
}

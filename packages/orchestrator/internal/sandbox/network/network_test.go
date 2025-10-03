package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	noopMetric "go.opentelemetry.io/otel/metric/noop"
)

func TestRoundTrip(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("skipping test as not root")
	}

	t.Setenv("USE_LOCAL_NAMESPACE_STORAGE", "true")

	ctx := t.Context()
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	t.Cleanup(cancel)

	nodeID := uuid.NewString()

	pool, err := NewPool(ctx, noopMetric.NewMeterProvider(), 1, 1, nodeID)
	require.NoError(t, err)
	t.Cleanup(func() {
		err = pool.Close(ctx)
		assert.NoError(t, err)
	})

	slot, err := pool.Get(ctx, false)
	require.NoError(t, err)

	// verify that the slot exists
	expectedPath := filepath.Join(netNamespacesDir, slot.NamespaceID())
	_, err = os.Stat(expectedPath)
	require.NoError(t, err)

	// return the slot to the pool
	err = pool.Return(ctx, slot)
	require.NoError(t, err)

	// verify that path still exists (slot was returned to the pool, but not released)
	_, err = os.Stat(expectedPath)
	require.NoError(t, err)

	// close the pool, release everything
	err = pool.Close(ctx)
	require.NoError(t, err)

	// verify that path is gone
	_, err = os.Stat(expectedPath)
	assert.True(t, os.IsNotExist(err))
}

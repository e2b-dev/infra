package block

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSliceDirectOutOfBoundsReturnsBytesNotAvailable(t *testing.T) {
	t.Parallel()

	cache, err := NewCache(16, 4, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	_, err = cache.sliceDirect(16, 4)
	require.ErrorIs(t, err, BytesNotAvailableError{})

	_, err = cache.sliceDirect(32, 4)
	require.ErrorIs(t, err, BytesNotAvailableError{})
}

//go:build linux

package build

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// makeLocalDiff writes `data` to a fresh local diff file and materializes it.
func makeLocalDiff(t *testing.T, blockSize int64, data []byte) Diff {
	t.Helper()

	f, err := NewLocalDiffFile(t.TempDir(), "build-id", Rootfs)
	require.NoError(t, err)
	_, err = f.WriteAt(data, 0)
	require.NoError(t, err)

	d, err := f.CloseToDiff(blockSize)
	require.NoError(t, err)

	return d
}

// TestDeferredDiff_IdentityIsSynchronous verifies CacheKey and BlockSize resolve
// without blocking on the (still-unresolved) inner promise, so DiffStore.Add and
// the upload's compress-config validation work the moment the diff is created.
func TestDeferredDiff_IdentityIsSynchronous(t *testing.T) {
	t.Parallel()

	inner := utils.NewSetOnce[Diff]()
	key := GetDiffStoreKey("build-id", Rootfs)
	d := NewDeferredDiff(key, 4096, inner)

	require.Equal(t, key, d.CacheKey())
	require.EqualValues(t, 4096, d.BlockSize())
}

// TestDeferredDiff_DelegatesAfterResolve verifies the data methods block until
// the promise resolves, then delegate to the materialized diff.
func TestDeferredDiff_DelegatesAfterResolve(t *testing.T) {
	t.Parallel()

	blockSize := int64(4096)
	data := make([]byte, blockSize)
	for i := range data {
		data[i] = 0x5A
	}

	real := makeLocalDiff(t, blockSize, data)
	t.Cleanup(func() { _ = real.Close() })

	inner := utils.NewSetOnce[Diff]()
	d := NewDeferredDiff(real.CacheKey(), blockSize, inner)

	// Resolve on a goroutine; the reads below block until it lands.
	go func() { _ = inner.SetValue(real) }()

	path, err := d.CachePath(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, path)

	sz, err := d.Size(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, blockSize, sz)

	buf := make([]byte, blockSize)
	_, err = d.ReadAt(t.Context(), buf, 0, nil)
	require.NoError(t, err)
	require.Equal(t, data, buf)
}

// TestDeferredDiff_PropagatesError verifies a failed seal surfaces through the
// data methods, and Close is a no-op (the producer cleans up the partial file).
func TestDeferredDiff_PropagatesError(t *testing.T) {
	t.Parallel()

	inner := utils.NewSetOnce[Diff]()
	d := NewDeferredDiff(GetDiffStoreKey("build-id", Rootfs), 4096, inner)

	sealErr := errors.New("seal failed")
	go func() { _ = inner.SetError(sealErr) }()

	_, err := d.CachePath(t.Context())
	require.ErrorIs(t, err, sealErr)

	require.NoError(t, d.Close(), "Close is a no-op when the seal failed")
}

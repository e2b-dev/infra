//go:build linux

package block

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// TestOverlayExportDiffInPlace_KeepsCacheAttached verifies the in-place export
// exports the dirty blocks WITHOUT detaching the cache (unlike EjectCache), so
// the overlay stays usable for a sandbox that keeps running after the export.
func TestOverlayExportDiffInPlace_KeepsCacheAttached(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(4)
	base := &fakeOriginalDevice{data: make([]byte, blockSize*numBlocks)}

	cache, err := NewCache(blockSize*numBlocks, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	o := NewOverlay(base, cache)

	// Dirty the first block through the overlay.
	blockA := make([]byte, blockSize)
	for i := range blockA {
		blockA[i] = 0xAA
	}
	_, err = o.WriteAt(blockA, 0)
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	dm, err := o.ExportDiffInPlace(t.Context(), out)
	require.NoError(t, err)
	require.EqualValues(t, 1, dm.Dirty.GetCardinality())

	// The dirty block landed in the diff.
	got, err := os.ReadFile(out.Name())
	require.NoError(t, err)
	require.Equal(t, blockA, got)

	// The cache was NOT ejected, so the overlay is still usable...
	require.False(t, o.cacheEjected.Load())

	blockB := make([]byte, blockSize)
	for i := range blockB {
		blockB[i] = 0xBB
	}
	_, err = o.WriteAt(blockB, blockSize) // second block
	require.NoError(t, err)

	readBack := make([]byte, blockSize)
	_, err = o.ReadAt(t.Context(), readBack, blockSize)
	require.NoError(t, err)
	require.Equal(t, blockB, readBack)

	// ...and Close still closes the cache (not a no-op as it is when ejected).
	require.NoError(t, o.Close())
}

// TestOverlayExportDiffInPlace_ErrsWhenEjected verifies the in-place export
// refuses to run once the cache has been ejected (the destroy path took it).
func TestOverlayExportDiffInPlace_ErrsWhenEjected(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	base := &fakeOriginalDevice{data: make([]byte, blockSize)}
	cache, err := NewCache(blockSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	o := NewOverlay(base, cache)
	_, err = o.EjectCache()
	require.NoError(t, err)

	out, err := os.CreateTemp(t.TempDir(), "diff-*")
	require.NoError(t, err)
	defer out.Close()

	_, err = o.ExportDiffInPlace(t.Context(), out)
	require.Error(t, err)
}

//go:build linux

package block

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// fill returns a blockSize-long slice of the given byte.
func fill(blockSize int64, b byte) []byte {
	buf := make([]byte, blockSize)
	for i := range buf {
		buf[i] = b
	}

	return buf
}

// TestOverlaySwapCache_ReadChain verifies that after swapping in a fresh cache,
// reads resolve through the three layers in order: writable cache (post-swap
// writes) → sealing cache (pre-swap writes) → base device (never written).
func TestOverlaySwapCache_ReadChain(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(4)

	// Base device: block 3 carries a distinctive pattern; the rest is zero.
	baseData := make([]byte, blockSize*numBlocks)
	copy(baseData[3*blockSize:4*blockSize], fill(blockSize, 0xCC))
	base := &fakeOriginalDevice{data: baseData}

	c0, err := NewCache(blockSize*numBlocks, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	o := NewOverlay(base, c0)

	// Pre-swap write to block 0.
	_, err = o.WriteAt(fill(blockSize, 0xAA), 0)
	require.NoError(t, err)

	// Swap in a fresh cache; c0 becomes the sealing cache.
	c1, err := NewCache(blockSize*numBlocks, blockSize, t.TempDir()+"/c1", false)
	require.NoError(t, err)
	old, err := o.SwapCache(c1)
	require.NoError(t, err)
	require.Same(t, c0, old)

	// Post-swap write to block 1 lands in c1.
	_, err = o.WriteAt(fill(blockSize, 0xBB), blockSize)
	require.NoError(t, err)

	read := func(blockIdx int64) []byte {
		buf := make([]byte, blockSize)
		_, rerr := o.ReadAt(t.Context(), buf, blockIdx*blockSize)
		require.NoError(t, rerr)

		return buf
	}

	require.Equal(t, fill(blockSize, 0xAA), read(0), "block 0 must resolve via the sealing cache")
	require.Equal(t, fill(blockSize, 0xBB), read(1), "block 1 must resolve via the writable cache")
	require.Equal(t, fill(blockSize, 0xCC), read(3), "block 3 must resolve via the base device")

	require.NoError(t, o.Close())
	// Both caches are closed by Close.
	require.True(t, c0.isClosed())
	require.True(t, c1.isClosed())
}

// TestOverlaySwapCache_RefusesSecondSwap verifies only one cache may be sealing
// at a time: a second SwapCache before ReleaseSealing errors.
func TestOverlaySwapCache_RefusesSecondSwap(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	base := &fakeOriginalDevice{data: make([]byte, blockSize)}
	c0, err := NewCache(blockSize, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	o := NewOverlay(base, c0)

	c1, err := NewCache(blockSize, blockSize, t.TempDir()+"/c1", false)
	require.NoError(t, err)
	_, err = o.SwapCache(c1)
	require.NoError(t, err)

	c2, err := NewCache(blockSize, blockSize, t.TempDir()+"/c2", false)
	require.NoError(t, err)
	_, err = o.SwapCache(c2)
	require.Error(t, err, "a second swap while sealing is outstanding must be refused")
	require.NoError(t, c2.Close())

	require.NoError(t, o.Close())
}

// TestOverlaySwapCache_ReleaseAllowsNextSwap verifies ReleaseSealing detaches
// the sealing cache (for the caller to close) and frees the slot for the next
// swap.
func TestOverlaySwapCache_ReleaseAllowsNextSwap(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	base := &fakeOriginalDevice{data: make([]byte, blockSize)}
	c0, err := NewCache(blockSize, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	o := NewOverlay(base, c0)

	c1, err := NewCache(blockSize, blockSize, t.TempDir()+"/c1", false)
	require.NoError(t, err)
	old, err := o.SwapCache(c1)
	require.NoError(t, err)
	require.Same(t, c0, old)

	// Release + close the sealing cache; the slot is now free.
	released := o.ReleaseSealing()
	require.Same(t, c0, released)
	require.NoError(t, released.Close())
	require.Nil(t, o.ReleaseSealing(), "second release returns nil")

	// A subsequent swap now succeeds.
	c2, err := NewCache(blockSize, blockSize, t.TempDir()+"/c2", false)
	require.NoError(t, err)
	old2, err := o.SwapCache(c2)
	require.NoError(t, err)
	require.Same(t, c1, old2)

	require.NoError(t, o.Close())
}

// TestOverlaySwapCache_ErrsWhenEjected verifies a swap is refused once the cache
// has been ejected by the destroy path.
func TestOverlaySwapCache_ErrsWhenEjected(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	base := &fakeOriginalDevice{data: make([]byte, blockSize)}
	c0, err := NewCache(blockSize, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c0.Close() })
	o := NewOverlay(base, c0)

	_, err = o.EjectCache()
	require.NoError(t, err)

	c1, err := NewCache(blockSize, blockSize, t.TempDir()+"/c1", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	_, err = o.SwapCache(c1)
	require.Error(t, err)
}

// TestOverlaySwapCache_ClosedSealingFallsThrough verifies the read safety net:
// if the sealing cache is closed out from under an in-flight read (as a collapse
// would do after a header rebase), ReadAt falls through to the base device
// instead of erroring.
func TestOverlaySwapCache_ClosedSealingFallsThrough(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)

	// Base serves block 0 (post-collapse it would carry the sealed bytes).
	baseData := fill(blockSize, 0xCC)
	base := &fakeOriginalDevice{data: baseData}

	c0, err := NewCache(blockSize, blockSize, t.TempDir()+"/c0", false)
	require.NoError(t, err)
	o := NewOverlay(base, c0)

	// Write block 0 into c0, then swap so c0 becomes sealing.
	_, err = o.WriteAt(fill(blockSize, 0xAA), 0)
	require.NoError(t, err)
	c1, err := NewCache(blockSize, blockSize, t.TempDir()+"/c1", false)
	require.NoError(t, err)
	_, err = o.SwapCache(c1)
	require.NoError(t, err)

	// Close the sealing cache directly, leaving the slot pointer set (the race
	// window between ReleaseSealing's swap and the caller's Close).
	require.NoError(t, c0.Close())

	buf := make([]byte, blockSize)
	_, err = o.ReadAt(t.Context(), buf, 0)
	require.NoError(t, err, "a closed sealing cache must not fail the read")
	require.Equal(t, fill(blockSize, 0xCC), buf, "read falls through to the base device")

	// Detach the already-closed sealing cache so Close doesn't double-close it.
	o.ReleaseSealing()
	require.NoError(t, o.Close())
}

package block

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestCache creates a minimal Cache for testing dirty-bit operations.
// It uses a small blockSize and does NOT create a real mmap — only the dirty
// array and blockSize are initialized.
func newTestCache(t *testing.T, numBlocks int64, blockSize int64) *Cache { //nolint:unparam // blockSize kept as param for test flexibility
	t.Helper()

	size := numBlocks * blockSize

	c, err := NewCache(size, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)

	t.Cleanup(func() { c.Close() })

	return c
}

func TestMarkBlockRangeCached_SingleBlock(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Block 0 should not be cached initially.
	require.False(t, c.isBlockCached(0))

	// Mark block 0 cached.
	c.markRangeCached(0, blockSize)
	require.True(t, c.isBlockCached(0))

	// Other blocks should still be uncached.
	require.False(t, c.isBlockCached(1))
	require.False(t, c.isBlockCached(2))
}

func TestMarkBlockRangeCached_MultipleBlocks(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Mark blocks 2..5 (4 blocks) cached.
	c.markRangeCached(2*blockSize, 4*blockSize)

	// Blocks 2..5 should all be cached.
	for i := int64(2); i < 6; i++ {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	// Blocks outside the range should not be cached.
	require.False(t, c.isBlockCached(0))
	require.False(t, c.isBlockCached(1))
	require.False(t, c.isBlockCached(6))
	require.False(t, c.isBlockCached(7))
}

func TestMarkBlockRangeCached_BoundaryCrossing(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	// Use 256 blocks to ensure we span word boundaries (word = 64 blocks).
	c := newTestCache(t, 256, blockSize)

	// Mark blocks 60..67 (crosses the 64-block word boundary).
	c.markRangeCached(60*blockSize, 8*blockSize)

	for i := int64(60); i < 68; i++ {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	// Boundary neighbors should not be cached.
	require.False(t, c.isBlockCached(59))
	require.False(t, c.isBlockCached(68))
}

func TestMarkBlockRangeCached_LargeRange(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 512
	c := newTestCache(t, numBlocks, blockSize)

	// Mark 200 blocks starting at block 50.
	c.markRangeCached(50*blockSize, 200*blockSize)

	for i := int64(50); i < 250; i++ {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	require.False(t, c.isBlockCached(49))
	require.False(t, c.isBlockCached(250))
}

func TestMarkBlockRangeCached_FirstBlock(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	c.markRangeCached(0, blockSize)
	require.True(t, c.isBlockCached(0))
	require.False(t, c.isBlockCached(1))
}

func TestMarkBlockRangeCached_LastBlock(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 128
	c := newTestCache(t, numBlocks, blockSize)

	c.markRangeCached((numBlocks-1)*blockSize, blockSize)
	require.True(t, c.isBlockCached(numBlocks-1))
	require.False(t, c.isBlockCached(numBlocks-2))
}

func TestMarkBlockRangeCached_EntireCache(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 256
	c := newTestCache(t, numBlocks, blockSize)

	c.markRangeCached(0, numBlocks*blockSize)

	for i := range numBlocks {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}
}

func TestDirtySortedKeys_Empty(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	keys := c.dirtySortedKeys()
	require.Empty(t, keys)
}

func TestDirtySortedKeys_Sorted(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 256, blockSize)

	// Mark blocks in non-sequential order.
	c.markRangeCached(100*blockSize, blockSize)
	c.markRangeCached(5*blockSize, blockSize)
	c.markRangeCached(200*blockSize, blockSize)
	c.markRangeCached(63*blockSize, blockSize)
	c.markRangeCached(64*blockSize, blockSize)

	keys := c.dirtySortedKeys()

	expected := []int64{
		5 * blockSize,
		63 * blockSize,
		64 * blockSize,
		100 * blockSize,
		200 * blockSize,
	}

	require.Equal(t, expected, keys)
	require.True(t, sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }))
}

func TestDirtySortedKeys_Range(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Mark blocks 10..14.
	c.markRangeCached(10*blockSize, 5*blockSize)

	keys := c.dirtySortedKeys()

	expected := []int64{
		10 * blockSize,
		11 * blockSize,
		12 * blockSize,
		13 * blockSize,
		14 * blockSize,
	}

	require.Equal(t, expected, keys)
}

func TestMarkBlockRangeCached_Idempotent(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Mark same block twice.
	c.markRangeCached(5*blockSize, blockSize)
	c.markRangeCached(5*blockSize, blockSize)

	require.True(t, c.isBlockCached(5))

	keys := c.dirtySortedKeys()
	require.Equal(t, []int64{5 * blockSize}, keys)
}

func TestMarkBlockRangeCached_OverlappingRanges(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Two overlapping ranges.
	c.markRangeCached(5*blockSize, 5*blockSize) // blocks 5..9
	c.markRangeCached(8*blockSize, 5*blockSize) // blocks 8..12

	// Union should be blocks 5..12.
	for i := int64(5); i <= 12; i++ {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	require.False(t, c.isBlockCached(4))
	require.False(t, c.isBlockCached(13))
}

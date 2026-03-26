package block

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// newTestCache creates a minimal Cache for testing dirty-bit operations.
// It uses a small blockSize and does NOT create a real mmap — only the dirty
// array and blockSize are initialized.
func newTestCache(t *testing.T, numBlocks int64, blockSize int64) *Cache {
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
	c.setIsCached(0, blockSize)
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
	c.setIsCached(2*blockSize, 4*blockSize)

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

	const blockSize int64 = 2 * 1024 * 1024 // 2 MiB hugepage block size
	// Use 256 blocks at 2 MiB to exercise a different block size and span word boundaries (word = 64 blocks).
	c := newTestCache(t, 256, blockSize)

	// Mark blocks 60..67 (crosses the 64-block word boundary).
	c.setIsCached(60*blockSize, 8*blockSize)

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
	c.setIsCached(50*blockSize, 200*blockSize)

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

	c.setIsCached(0, blockSize)
	require.True(t, c.isBlockCached(0))
	require.False(t, c.isBlockCached(1))
}

func TestMarkBlockRangeCached_LastBlock(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 128
	c := newTestCache(t, numBlocks, blockSize)

	c.setIsCached((numBlocks-1)*blockSize, blockSize)
	require.True(t, c.isBlockCached(numBlocks-1))
	require.False(t, c.isBlockCached(numBlocks-2))
}

func TestMarkBlockRangeCached_EntireCache(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 256
	c := newTestCache(t, numBlocks, blockSize)

	c.setIsCached(0, numBlocks*blockSize)

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
	c.setIsCached(100*blockSize, blockSize)
	c.setIsCached(5*blockSize, blockSize)
	c.setIsCached(200*blockSize, blockSize)
	c.setIsCached(63*blockSize, blockSize)
	c.setIsCached(64*blockSize, blockSize)

	keys := c.dirtySortedKeys()

	expected := []int64{
		5 * blockSize,
		63 * blockSize,
		64 * blockSize,
		100 * blockSize,
		200 * blockSize,
	}

	require.Equal(t, expected, keys)
}

func TestDirtySortedKeys_Range(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Mark blocks 10..14.
	c.setIsCached(10*blockSize, 5*blockSize)

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
	c.setIsCached(5*blockSize, blockSize)
	c.setIsCached(5*blockSize, blockSize)

	require.True(t, c.isBlockCached(5))

	keys := c.dirtySortedKeys()
	require.Equal(t, []int64{5 * blockSize}, keys)
}

func TestMarkBlockRangeCached_OverlappingRanges(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	c := newTestCache(t, 128, blockSize)

	// Two overlapping ranges.
	c.setIsCached(5*blockSize, 5*blockSize) // blocks 5..9
	c.setIsCached(8*blockSize, 5*blockSize) // blocks 8..12

	// Union should be blocks 5..12.
	for i := int64(5); i <= 12; i++ {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	require.False(t, c.isBlockCached(4))
	require.False(t, c.isBlockCached(13))
}

func TestSetIsCached_PastCacheSize(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 128
	c := newTestCache(t, numBlocks, blockSize)

	// Pass a range that extends past cache size — should not panic.
	c.setIsCached((numBlocks-2)*blockSize, 10*blockSize)

	// Last two blocks should be cached.
	require.True(t, c.isBlockCached(numBlocks-2))
	require.True(t, c.isBlockCached(numBlocks-1))
}

func TestSetIsCached_ConcurrentOverlapping(t *testing.T) {
	t.Parallel()

	const blockSize int64 = 4096
	const numBlocks int64 = 512
	c := newTestCache(t, numBlocks, blockSize)

	// 8 goroutines each mark an overlapping 128-block range.
	// Ranges: [0,128), [32,160), [64,192), ... [224,352)
	const goroutines = 8
	const rangeBlocks int64 = 128
	const strideBlocks int64 = 32

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func() {
			defer wg.Done()
			off := int64(g) * strideBlocks * blockSize
			c.setIsCached(off, rangeBlocks*blockSize)
		}()
	}
	wg.Wait()

	// Union covers blocks 0..(goroutines-1)*stride+rangeBlocks-1.
	lastBlock := int64(goroutines-1)*strideBlocks + rangeBlocks
	for i := range lastBlock {
		require.True(t, c.isBlockCached(i), "block %d should be cached", i)
	}

	require.False(t, c.isBlockCached(lastBlock))
}

// --- Benchmarks ---

const (
	benchBlockSize  int64 = 4096
	benchNumBlocks  int64 = 16384             // 64 MiB at 4K blocks — realistic memfile size
	benchCacheSize  int64 = benchNumBlocks * benchBlockSize
	benchChunkSize  int64 = 4 * 1024 * 1024   // 4 MiB — MemoryChunkSize
	benchChunkCount int64 = benchCacheSize / benchChunkSize
)

func newBenchCache(b *testing.B) *Cache {
	b.Helper()

	c, err := NewCache(benchCacheSize, benchBlockSize, filepath.Join(b.TempDir(), "cache"), false)
	if err != nil {
		b.Fatal(err)
	}

	b.Cleanup(func() { c.Close() })

	return c
}

// BenchmarkMarkRangeCached benchmarks marking a 4 MiB chunk (1024 blocks) as cached.
func BenchmarkMarkRangeCached(b *testing.B) {
	c := newBenchCache(b)

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(benchChunkCount)) * benchChunkSize
		c.setIsCached(off, benchChunkSize)
	}
}

// BenchmarkIsCached_Hit benchmarks checking a cached 4 MiB range.
func BenchmarkIsCached_Hit(b *testing.B) {
	c := newBenchCache(b)
	c.setIsCached(0, benchCacheSize) // everything cached

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(benchChunkCount)) * benchChunkSize
		if !c.isCached(off, benchChunkSize) {
			b.Fatal("expected cached")
		}
	}
}

// BenchmarkIsCached_Miss benchmarks checking an uncached range.
func BenchmarkIsCached_Miss(b *testing.B) {
	c := newBenchCache(b)
	// leave everything uncached

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(benchChunkCount)) * benchChunkSize
		if c.isCached(off, benchChunkSize) {
			b.Fatal("expected not cached")
		}
	}
}

// BenchmarkSlice_Hit benchmarks Slice on a fully-cached dirty-file=false cache.
// This is the hot path in the chunker: check bitmap, return mmap slice.
func BenchmarkSlice_Hit(b *testing.B) {
	c := newBenchCache(b)
	c.setIsCached(0, benchCacheSize)

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(benchChunkCount)) * benchChunkSize
		s, err := c.Slice(off, benchChunkSize)
		if err != nil {
			b.Fatal(err)
		}
		if len(s) == 0 {
			b.Fatal("empty slice")
		}
	}
}

// BenchmarkSlice_Miss benchmarks Slice on an uncached cache (returns BytesNotAvailableError).
func BenchmarkSlice_Miss(b *testing.B) {
	c := newBenchCache(b)

	b.ResetTimer()
	for i := range b.N {
		off := int64(i%int(benchChunkCount)) * benchChunkSize
		_, err := c.Slice(off, benchChunkSize)
		if err == nil {
			b.Fatal("expected error")
		}
	}
}

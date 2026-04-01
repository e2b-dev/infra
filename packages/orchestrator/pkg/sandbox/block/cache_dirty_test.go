package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TestIsCached_ZeroSizeCache(t *testing.T) {
	t.Parallel()

	// Create a zero-size cache (triggers the size == 0 branch in NewCache)
	cache, err := NewCache(0, header.PageSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// With size=0, any isCached call should return false (or handle gracefully)
	// The bug: end = min(off+length, 0) = 0, then end-1 = -1 causes underflow
	// Original behavior: returned true for empty range (no blocks to check)

	// This should not panic and should return a sensible value
	result := cache.isCached(0, 100)
	// With zero-size cache, nothing can be cached
	assert.False(t, result, "zero-size cache should report nothing as cached")
}

func TestIsCached_ZeroLength(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Zero-length query should return true (vacuously true - no blocks to check)
	// This matches the original sync.Map behavior where BlocksOffsets(0, blockSize)
	// returned an empty slice, so the loop didn't execute and returned true.
	result := cache.isCached(0, 0)
	assert.True(t, result, "zero-length isCached should return true (vacuously)")

	result = cache.isCached(blockSize*5, 0)
	assert.True(t, result, "zero-length isCached at any offset should return true")
}

func TestSetIsCached_ZeroLength(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Zero-length setIsCached should be a no-op
	// The bug: off=0, length=0 -> off+length-1 = -1 -> BlockIdx(-1, blockSize) = 0
	// This incorrectly marks block 0 as dirty
	cache.setIsCached(0, 0)

	// Block 0 should NOT be marked as cached after a zero-length set
	// We need to check directly since isCached(0, 0) returns true vacuously
	result := cache.isCached(0, blockSize)
	assert.False(t, result, "zero-length setIsCached should not mark any blocks as dirty")
}

func TestSetIsCached_ZeroLengthAtNonZeroOffset(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Zero-length at offset in block 5 should not mark anything
	cache.setIsCached(blockSize*5, 0)

	// No blocks should be marked as cached
	for i := range int64(10) {
		result := cache.isCached(i*blockSize, blockSize)
		assert.False(t, result, "block %d should not be cached after zero-length setIsCached", i)
	}
}

func TestWriteAt_EmptyData(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Writing empty data should not mark any blocks as dirty
	n, err := cache.WriteAt([]byte{}, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Block 0 should NOT be marked as cached
	result := cache.isCached(0, blockSize)
	assert.False(t, result, "empty WriteAt should not mark block 0 as dirty")
}

func TestWriteAt_PartiallyBeyondCacheSize(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Writing at the last block with data extending beyond cache size
	// should only write up to cache size and mark appropriate blocks
	data := make([]byte, blockSize*2)          // 2 blocks of data
	n, err := cache.WriteAt(data, blockSize*9) // Start at block 9, would extend to block 11
	require.NoError(t, err)
	assert.Equal(t, int(blockSize), n, "should only write up to cache size")

	// Only block 9 should be marked as cached (the portion that fit)
	for i := range int64(10) {
		if i == 9 {
			assert.True(t, cache.isCached(i*blockSize, blockSize), "block 9 should be cached")
		} else {
			assert.False(t, cache.isCached(i*blockSize, blockSize), "block %d should not be cached", i)
		}
	}
}

func TestIsCached_OffsetBeyondSize(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Query beyond cache size should return false
	result := cache.isCached(cacheSize+blockSize, blockSize)
	assert.False(t, result, "isCached beyond cache size should return false")
}

func TestDirtyTracking_NormalOperation(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Initially nothing is cached
	for i := range int64(10) {
		assert.False(t, cache.isCached(i*blockSize, blockSize), "block %d should not be initially cached", i)
	}

	// Write to block 3
	data := make([]byte, blockSize)
	n, err := cache.WriteAt(data, blockSize*3)
	require.NoError(t, err)
	assert.Equal(t, int(blockSize), n)

	// Only block 3 should be cached
	for i := range int64(10) {
		if i == 3 {
			assert.True(t, cache.isCached(i*blockSize, blockSize), "block 3 should be cached")
		} else {
			assert.False(t, cache.isCached(i*blockSize, blockSize), "block %d should not be cached", i)
		}
	}

	// Write spanning blocks 5-7
	multiBlockData := make([]byte, blockSize*3)
	n, err = cache.WriteAt(multiBlockData, blockSize*5)
	require.NoError(t, err)
	assert.Equal(t, int(blockSize*3), n)

	// Blocks 3, 5, 6, 7 should be cached
	expected := map[int64]bool{3: true, 5: true, 6: true, 7: true}
	for i := range int64(10) {
		if expected[i] {
			assert.True(t, cache.isCached(i*blockSize, blockSize), "block %d should be cached", i)
		} else {
			assert.False(t, cache.isCached(i*blockSize, blockSize), "block %d should not be cached", i)
		}
	}
}

func TestIsCached_NegativeOffset(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Mark block 0 as cached
	cache.setIsCached(0, blockSize)

	// Negative offset should return false, not check block 0
	// This prevents BlockIdx(-1, blockSize) = 0 from causing false positives
	result := cache.isCached(-1, blockSize)
	assert.False(t, result, "negative offset should return false, not check block 0")

	result = cache.isCached(-blockSize+1, blockSize)
	assert.False(t, result, "negative offset in (-blockSize, 0) should return false")
}

func TestSetIsCached_NegativeOffset(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Setting with negative offset should be a no-op
	cache.setIsCached(-1, blockSize)

	// Block 0 should NOT be marked as cached
	result := cache.isCached(0, blockSize)
	assert.False(t, result, "negative offset setIsCached should not mark block 0")
}

func TestWriteAt_NegativeOffset(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Writing at negative offset should return 0, no panic
	data := make([]byte, blockSize)
	n, err := cache.WriteAt(data, -1)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "negative offset WriteAt should return 0 bytes")

	// Block 0 should NOT be marked as cached
	result := cache.isCached(0, blockSize)
	assert.False(t, result, "negative offset WriteAt should not mark block 0")
}

func TestWriteAt_OffsetBeyondCacheSize(t *testing.T) {
	t.Parallel()

	const blockSize = int64(header.PageSize)
	const cacheSize = blockSize * 10

	cache, err := NewCache(cacheSize, blockSize, t.TempDir()+"/cache", false)
	require.NoError(t, err)
	defer cache.Close()

	// Writing at offset beyond cache size should return 0 bytes written, no error, no panic
	data := make([]byte, blockSize)
	n, err := cache.WriteAt(data, cacheSize+blockSize)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "should write 0 bytes when offset is beyond cache size")

	// No blocks should be marked as cached
	for i := range int64(10) {
		assert.False(t, cache.isCached(i*blockSize, blockSize), "block %d should not be cached", i)
	}
}

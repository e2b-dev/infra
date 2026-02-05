package block

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestCompressMMapLRUChunker_TwoLevelCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := make([]byte, frameSizeU)
	for i := range uncompressedData {
		uncompressedData[i] = byte(i % 256)
	}
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		frameSizeU,
		int64(len(compressedData)),
		mockGetter,
		"test/path",
		cachePath,
		1, // Small LRU to force evictions
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// First read - fetches from storage, stores in mmap, decompresses to LRU
	buf := make([]byte, 100)
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:100], buf)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// Purge LRU to simulate eviction
	chunker.frameLRU.Purge()

	// Read again - should NOT fetch from storage (should decompress from mmap)
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:100], buf)
	// Still only 1 call - compressed data is in mmap cache
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)
}

func TestCompressMMapLRUChunker_MultipleFrames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024) // 4MB per frame
	totalSize := frameSizeU * 2          // 2 frames

	// Create data for two frames
	data1 := make([]byte, frameSizeU)
	data2 := make([]byte, frameSizeU)
	for i := range data1 {
		data1[i] = byte(i % 256)
		data2[i] = byte((i + 100) % 256)
	}

	compressed1 := compressData(t, data1)
	compressed2 := compressData(t, data2)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressed1))},
			{U: int32(frameSizeU), C: int32(len(compressed2))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{
		0:          compressed1,
		frameSizeU: compressed2,
	})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		totalSize,
		int64(len(compressed1)+len(compressed2)),
		mockGetter,
		"test/path",
		cachePath,
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read from first frame
	buf := make([]byte, 100)
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data1[:100], buf)

	// Read from second frame
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, frameSizeU, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data2[:100], buf)

	// Both frames should have been fetched
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2)
}

func TestCompressMMapLRUChunker_LRUEvictionUsesLocalMmap(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024) // 4MB per frame
	totalSize := frameSizeU * 3          // 3 frames

	// Create data for three frames
	data1 := make([]byte, frameSizeU)
	data2 := make([]byte, frameSizeU)
	data3 := make([]byte, frameSizeU)
	for i := range data1 {
		data1[i] = byte(i % 256)
		data2[i] = byte((i + 100) % 256)
		data3[i] = byte((i + 200) % 256)
	}

	compressed1 := compressData(t, data1)
	compressed2 := compressData(t, data2)
	compressed3 := compressData(t, data3)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressed1))},
			{U: int32(frameSizeU), C: int32(len(compressed2))},
			{U: int32(frameSizeU), C: int32(len(compressed3))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{
		0:              compressed1,
		frameSizeU:     compressed2,
		frameSizeU * 2: compressed3,
	})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		totalSize,
		int64(len(compressed1)+len(compressed2)+len(compressed3)),
		mockGetter,
		"test/path",
		cachePath,
		1, // LRU of 1 forces eviction
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	buf := make([]byte, 100)

	// Read frame 1 - fetches from storage
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data1[:100], buf)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// Read frame 2 - evicts frame 1 from LRU
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, frameSizeU, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data2[:100], buf)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2)

	// Read frame 3 - evicts frame 2 from LRU
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, frameSizeU*2, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data3[:100], buf)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)

	// Read frame 1 again - evicted from LRU but still in mmap, no storage fetch
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data1[:100], buf)
	// Still only 3 calls - compressed data is in mmap cache
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)

	// Read frame 2 again - also from mmap
	_, err = func() (int, error) {
		s, e := chunker.Slice(ctx, frameSizeU, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, data2[:100], buf)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)
}

func TestCompressMMapLRUChunker_Close(t *testing.T) {
	t.Parallel()

	frameSizeU := int64(4 * 1024 * 1024)
	compressedData := compressData(t, make([]byte, frameSizeU))

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		frameSizeU,
		int64(len(compressedData)),
		mockGetter,
		"test/path",
		cachePath,
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	err = chunker.Close()
	require.NoError(t, err)

	// LRU should be purged
	assert.Equal(t, 0, chunker.frameLRU.Len())
}

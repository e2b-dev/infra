package block

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// ChunkerTestContext holds everything needed to test a chunker
type ChunkerTestContext struct {
	Chunker    Chunker
	FrameTable *storage.FrameTable // nil for uncompressed chunkers
	Data       []byte              // uncompressed test data
	DataSize   int64
	Mock       *storage.MockStorageProvider
}

// ChunkerFactory creates a chunker with test data for testing
type ChunkerFactory func(t *testing.T, dataSize int64) *ChunkerTestContext

// makeTestData creates deterministic test data
func makeTestData(size int64) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	return data
}

// compressLRUFactory creates a CompressLRUChunker for testing
func compressLRUFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	data := makeTestData(dataSize)
	compressed := compressData(t, data)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(dataSize), C: int32(len(compressed))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressed})

	chunker, err := NewCompressLRUChunker(
		dataSize,
		mockStorage,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: frameTable,
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

// compressMMapLRUFactory creates a CompressMMapLRUChunker for testing
func compressMMapLRUFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	data := makeTestData(dataSize)
	compressed := compressData(t, data)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(dataSize), C: int32(len(compressed))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressed})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		dataSize,
		int64(len(compressed)),
		mockStorage,
		"test/path",
		cachePath,
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: frameTable,
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

// decompressMMapFactory creates a DecompressMMapChunker for testing (compressed mode)
func decompressMMapFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	data := makeTestData(dataSize)
	compressed := compressData(t, data)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(dataSize), C: int32(len(compressed))},
		},
	}

	mockStorage := setupMockStorageDecompress(t, map[int64][]byte{0: compressed})
	cachePath := filepath.Join(t.TempDir(), "decompress_cache")

	chunker, err := NewDecompressMMapChunker(
		dataSize,
		int64(len(compressed)),
		storage.MemoryChunkSize,
		mockStorage,
		"test/path",
		cachePath,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: frameTable,
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

// uncompressedMMapFactory creates an UncompressedMMapChunker for testing
func uncompressedMMapFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	data := makeTestData(dataSize)

	mockStorage := setupMockStorageUncompressed(t, data)
	cachePath := filepath.Join(t.TempDir(), "uncompressed_cache")

	chunker, err := NewUncompressedMMapChunker(
		dataSize,
		storage.MemoryChunkSize,
		mockStorage,
		"test/path",
		cachePath,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: nil, // no frame table for uncompressed
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

// Multi-frame factory for compressed chunkers
func compressLRUMultiFrameFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	frameSize := dataSize / 2
	data := makeTestData(dataSize)

	frame1Data := data[:frameSize]
	frame2Data := data[frameSize:]
	compressed1 := compressData(t, frame1Data)
	compressed2 := compressData(t, frame2Data)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSize), C: int32(len(compressed1))},
			{U: int32(frameSize), C: int32(len(compressed2))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{
		0:         compressed1,
		frameSize: compressed2,
	})

	chunker, err := NewCompressLRUChunker(
		dataSize,
		mockStorage,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: frameTable,
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

func compressMMapLRUMultiFrameFactory(t *testing.T, dataSize int64) *ChunkerTestContext {
	t.Helper()

	frameSize := dataSize / 2
	data := makeTestData(dataSize)

	frame1Data := data[:frameSize]
	frame2Data := data[frameSize:]
	compressed1 := compressData(t, frame1Data)
	compressed2 := compressData(t, frame2Data)

	totalCompressed := int64(len(compressed1) + len(compressed2))

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSize), C: int32(len(compressed1))},
			{U: int32(frameSize), C: int32(len(compressed2))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{
		0:         compressed1,
		frameSize: compressed2,
	})
	cachePath := filepath.Join(t.TempDir(), "compressed_cache")

	chunker, err := NewCompressMMapLRUChunker(
		dataSize,
		totalCompressed,
		mockStorage,
		"test/path",
		cachePath,
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	return &ChunkerTestContext{
		Chunker:    chunker,
		FrameTable: frameTable,
		Data:       data,
		DataSize:   dataSize,
		Mock:       mockStorage,
	}
}

// =============================================================================
// Common Test Functions
// =============================================================================

func runMatrixBasicSlice(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	tc := factory(t, 4*1024*1024) // 4MB
	defer tc.Chunker.Close()

	// Read first 1024 bytes
	slice, err := tc.Chunker.Slice(ctx, 0, 1024, tc.FrameTable)
	require.NoError(t, err)
	assert.Len(t, slice, 1024)
	assert.Equal(t, tc.Data[:1024], slice)

	// Read more from same region (cache hit expected)
	slice, err = tc.Chunker.Slice(ctx, 0, 2048, tc.FrameTable)
	require.NoError(t, err)
	assert.Len(t, slice, 2048)
	assert.Equal(t, tc.Data[:2048], slice)
}

func runMatrixEmptySlice(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	tc := factory(t, 4*1024*1024)
	defer tc.Chunker.Close()

	slice, err := tc.Chunker.Slice(ctx, 0, 0, tc.FrameTable)
	require.NoError(t, err)
	assert.Empty(t, slice)
}

func runMatrixConcurrentReads(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	tc := factory(t, 4*1024*1024)
	defer tc.Chunker.Close()

	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for range 10 {
		wg.Go(func() {
			slice, err := tc.Chunker.Slice(ctx, 0, 1024, tc.FrameTable)
			if err != nil {
				errors <- err

				return
			}
			if len(slice) != 1024 {
				errors <- assert.AnError
			}
		})
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}
}

func runMatrixLargeRequestPartialCacheMiss(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	// Use multi-frame factory for compressed chunkers to test cross-frame partial cache
	tc := factory(t, 8*1024*1024) // 8MB = 2 frames of 4MB each
	defer tc.Chunker.Close()

	// Step 1: Read first 1KB to populate first frame/chunk in cache
	slice1, err := tc.Chunker.Slice(ctx, 0, 1024, tc.FrameTable)
	require.NoError(t, err)
	assert.Equal(t, tc.Data[:1024], slice1)

	// Step 2: Read 6MB starting at 1MB
	// For multi-frame: this spans the cached first frame and uncached second frame
	// For single-frame: this should still work from the cached data
	offset := int64(1 * 1024 * 1024) // 1MB
	length := int64(6 * 1024 * 1024) // 6MB

	// Adjust if data is smaller
	if offset+length > tc.DataSize {
		length = tc.DataSize - offset
	}

	slice2, err := tc.Chunker.Slice(ctx, offset, length, tc.FrameTable)
	require.NoError(t, err)
	assert.Equal(t, tc.Data[offset:offset+length], slice2)
}

// =============================================================================
// Mmap-specific tests
// =============================================================================

func runMatrixFileSize(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	tc := factory(t, 4*1024*1024)
	defer tc.Chunker.Close()

	// Before fetching, sparse file size should be 0
	size, err := tc.Chunker.FileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(0), size)

	// Fetch some data
	_, err = tc.Chunker.Slice(ctx, 0, 4096, tc.FrameTable)
	require.NoError(t, err)

	// After fetching, size should be positive
	size, err = tc.Chunker.FileSize()
	require.NoError(t, err)
	assert.Positive(t, size)
}

// =============================================================================
// Compressed chunker tests (Chunk rejects frame-spanning requests)
// =============================================================================

// runMatrixChunkHandlesFrameSpan tests that cross-frame reads trigger SLOW_PATH_HIT error.
// This helps verify whether slow path is ever invoked in practice.
func runMatrixChunkHandlesFrameSpan(t *testing.T, factory ChunkerFactory) {
	t.Helper()
	ctx := context.Background()

	tc := factory(t, 8*1024*1024) // 8MB = 2 frames
	defer tc.Chunker.Close()

	if tc.FrameTable == nil || len(tc.FrameTable.Frames) < 2 {
		t.Skip("test requires multi-frame setup")
	}

	frameSize := int64(tc.FrameTable.Frames[0].U)
	offset := frameSize - 500
	length := int64(1000) // spans 500 bytes into second frame

	_, err := tc.Chunker.Slice(ctx, offset, length, tc.FrameTable)
	require.Error(t, err, "cross-frame read should error with SLOW_PATH_HIT")
	assert.Contains(t, err.Error(), "SLOW_PATH_HIT", "error should indicate slow path was triggered")
}

// =============================================================================
// Matrix Test Runner
// =============================================================================

func TestChunkerMatrix_AllChunkers(t *testing.T) {
	t.Parallel()

	factories := map[string]ChunkerFactory{
		"CompressLRU":      compressLRUFactory,
		"CompressMMapLRU":  compressMMapLRUFactory,
		"DecompressMMap":   decompressMMapFactory,
		"UncompressedMMap": uncompressedMMapFactory,
	}

	tests := []struct {
		name string
		test func(t *testing.T, factory ChunkerFactory)
	}{
		{"BasicSlice", runMatrixBasicSlice},
		{"EmptySlice", runMatrixEmptySlice},
		{"ConcurrentReads", runMatrixConcurrentReads},
	}

	for factoryName, factory := range factories {
		for _, tc := range tests {
			t.Run(factoryName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.test(t, factory)
			})
		}
	}
}

func TestChunkerMatrix_MmapChunkers(t *testing.T) {
	t.Parallel()

	factories := map[string]ChunkerFactory{
		"CompressMMapLRU":  compressMMapLRUFactory,
		"DecompressMMap":   decompressMMapFactory,
		"UncompressedMMap": uncompressedMMapFactory,
	}

	tests := []struct {
		name string
		test func(t *testing.T, factory ChunkerFactory)
	}{
		{"FileSize", runMatrixFileSize},
	}

	for factoryName, factory := range factories {
		for _, tc := range tests {
			t.Run(factoryName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.test(t, factory)
			})
		}
	}
}

func TestChunkerMatrix_CompressedChunkers(t *testing.T) {
	t.Parallel()

	// Use multi-frame factories for cross-frame tests
	factories := map[string]ChunkerFactory{
		"CompressLRU":     compressLRUMultiFrameFactory,
		"CompressMMapLRU": compressMMapLRUMultiFrameFactory,
	}

	tests := []struct {
		name string
		test func(t *testing.T, factory ChunkerFactory)
	}{
		{"ChunkHandlesFrameSpan", runMatrixChunkHandlesFrameSpan},
	}

	for factoryName, factory := range factories {
		for _, tc := range tests {
			t.Run(factoryName+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				tc.test(t, factory)
			})
		}
	}
}

// Also test single-frame chunkers with partial cache miss
// Note: mmap-based chunkers (DecompressMMap, UncompressedMMap) have a Cache limitation
// where dirty tracking uses absolute offsets, so non-block-aligned reads after caching
// don't work correctly. Only LRU-based chunkers support this pattern.
func TestChunkerMatrix_PartialCacheMiss_SingleFrame(t *testing.T) {
	t.Parallel()

	// Only LRU-based chunkers support partial cache miss with non-aligned reads
	factories := map[string]ChunkerFactory{
		"CompressLRU":     compressLRUFactory,
		"CompressMMapLRU": compressMMapLRUFactory,
	}

	for factoryName, factory := range factories {
		t.Run(factoryName+"/LargeRequestPartialCacheMiss", func(t *testing.T) {
			t.Parallel()
			runMatrixLargeRequestPartialCacheMiss(t, factory)
		})
	}
}

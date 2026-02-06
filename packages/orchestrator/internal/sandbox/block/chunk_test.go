package block

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// =============================================================================
// Helpers
// =============================================================================

// compressData compresses data using zstd
func compressData(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	defer enc.Close()

	return enc.EncodeAll(data, nil)
}

func testMetrics(t *testing.T) metrics.Metrics {
	t.Helper()
	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	return m
}

// makeTestData creates deterministic test data
func makeTestData(size int64) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	return data
}

// setupMockStorage creates a MockStorageProvider that returns compressed frames from a map
func setupMockStorage(t *testing.T, frames map[int64][]byte) *storage.MockStorageProvider {
	t.Helper()
	mockStorage := storage.NewMockStorageProvider(t)

	mockStorage.EXPECT().
		GetFrame(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, offsetU int64, _ *storage.FrameTable, decompress bool, buf []byte) (storage.Range, error) {
			data, ok := frames[offsetU]
			if !ok {
				return storage.Range{}, nil
			}

			if decompress {
				dec, err := zstd.NewReader(nil)
				if err != nil {
					return storage.Range{}, err
				}
				defer dec.Close()

				decompressed, err := dec.DecodeAll(data, nil)
				if err != nil {
					return storage.Range{}, err
				}

				n := copy(buf, decompressed)

				return storage.Range{Start: offsetU, Length: n}, nil
			}

			n := copy(buf, data)

			return storage.Range{Start: offsetU, Length: n}, nil
		}).Maybe()

	return mockStorage
}

// setupMockStorageUncompressed creates a MockStorageProvider for UncompressedMMapChunker tests.
// It returns uncompressed data directly (no frame table, no decompression).
func setupMockStorageUncompressed(t *testing.T, data []byte) *storage.MockStorageProvider {
	t.Helper()
	mockStorage := storage.NewMockStorageProvider(t)

	mockStorage.EXPECT().
		GetFrame(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, offset int64, ft *storage.FrameTable, decompress bool, buf []byte) (storage.Range, error) {
			// UncompressedMMapChunker always passes nil frameTable and false for decompress
			assert.Nil(t, ft, "UncompressedMMapChunker should pass nil frameTable")
			assert.False(t, decompress, "UncompressedMMapChunker should pass decompress=false")

			// Return data from the requested offset
			end := min(offset+int64(len(buf)), int64(len(data)))
			if offset >= int64(len(data)) {
				return storage.Range{Start: offset, Length: 0}, nil
			}

			n := copy(buf, data[offset:end])

			return storage.Range{Start: offset, Length: n}, nil
		}).Maybe()

	return mockStorage
}

// =============================================================================
// CompressLRUChunker Tests
// =============================================================================

func TestCompressLRUChunker_LRUPopulation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data - one frame of 8MB (2 chunks)
	frameSizeU := int64(8 * 1024 * 1024)
	uncompressedData := makeTestData(frameSizeU)
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})

	chunker, err := NewCompressLRUChunker(
		frameSizeU,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read from the frame
	_, err = chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)

	// One frame should be in LRU
	lruCount, _ := chunker.LRUStats()
	assert.Equal(t, 1, lruCount)

	// Reading from another part of the same frame should not trigger another fetch
	_, err = chunker.Slice(ctx, storage.MemoryChunkSize, 100, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)
}

func TestCompressLRUChunker_LRUEvictionRefetch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := makeTestData(frameSizeU)
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})

	chunker, err := NewCompressLRUChunker(
		frameSizeU,
		mockGetter,
		"test/path",
		1, // Small LRU
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// First read - fetches from storage
	_, err = chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// LRU should have the frame
	lruCount, _ := chunker.LRUStats()
	assert.Equal(t, 1, lruCount)

	// Purge LRU to simulate eviction
	chunker.frameLRU.Purge()

	// Read again - must re-fetch from storage (NFS cache would handle file caching in production)
	_, err = chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2) // Re-fetched after LRU eviction
}

func TestCompressLRUChunker_SliceAcrossChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data spanning multiple chunks
	frameSizeU := int64(8 * 1024 * 1024) // 8MB = 2 chunks
	uncompressedData := makeTestData(frameSizeU)
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})

	chunker, err := NewCompressLRUChunker(
		frameSizeU,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read across chunk boundary
	offset := int64(storage.MemoryChunkSize - 500) // 500 bytes before chunk boundary
	length := int64(1000)                          // spans into second chunk

	slice, err := chunker.Slice(ctx, offset, length, frameTable)
	require.NoError(t, err)
	assert.Len(t, slice, int(length))
	assert.Equal(t, uncompressedData[offset:offset+length], slice)
}

func TestCompressLRUChunker_MultipleFrames(t *testing.T) {
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

	chunker, err := NewCompressLRUChunker(
		totalSize,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read from first frame
	s, err := chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data1[:100], s)

	// Read from second frame
	s, err = chunker.Slice(ctx, frameSizeU, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data2[:100], s)

	// Both frames should have been fetched
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2)
}

func TestCompressLRUChunker_Close(t *testing.T) {
	t.Parallel()

	frameSizeU := int64(4 * 1024 * 1024)
	compressedData := compressData(t, make([]byte, frameSizeU))

	mockGetter := setupMockStorage(t, map[int64][]byte{0: compressedData})

	chunker, err := NewCompressLRUChunker(
		frameSizeU,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)

	err = chunker.Close()
	require.NoError(t, err)

	// LRU should be purged
	assert.Equal(t, 0, chunker.frameLRU.Len())
}

// =============================================================================
// CompressMMapLRUChunker Tests
// =============================================================================

func TestCompressMMapLRUChunker_TwoLevelCache(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := makeTestData(frameSizeU)
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
	s, err := chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:100], s)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// Purge LRU to simulate eviction
	chunker.frameLRU.Purge()

	// Read again - should NOT fetch from storage (should decompress from mmap)
	s, err = chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:100], s)
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
	s, err := chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data1[:100], s)

	// Read from second frame
	s, err = chunker.Slice(ctx, frameSizeU, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data2[:100], s)

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

	// Read frame 1 - fetches from storage
	s, err := chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data1[:100], s)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// Read frame 2 - evicts frame 1 from LRU
	s, err = chunker.Slice(ctx, frameSizeU, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data2[:100], s)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2)

	// Read frame 3 - evicts frame 2 from LRU
	s, err = chunker.Slice(ctx, frameSizeU*2, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data3[:100], s)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)

	// Read frame 1 again - evicted from LRU but still in mmap, no storage fetch
	s, err = chunker.Slice(ctx, 0, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data1[:100], s)
	// Still only 3 calls - compressed data is in mmap cache
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)

	// Read frame 2 again - also from mmap
	s, err = chunker.Slice(ctx, frameSizeU, 100, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data2[:100], s)
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

// =============================================================================
// DecompressMMapChunker Tests
// =============================================================================

func TestDecompressMMapChunker_ReadAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := makeTestData(frameSizeU)
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressedData})

	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache")

	chunker, err := NewDecompressMMapChunker(
		frameSizeU,
		int64(len(compressedData)),
		storage.MemoryChunkSize,
		mockStorage,
		"test/path",
		cachePath,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Use block-aligned offset (NBD always sends aligned requests)
	s, err := chunker.Slice(ctx, 0, 2048, frameTable)
	require.NoError(t, err)
	assert.Len(t, s, 2048)
	assert.Equal(t, uncompressedData[0:2048], s)
}

func TestDecompressMMapChunker_CachePersists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := makeTestData(frameSizeU)
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressedData})

	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache")

	chunker, err := NewDecompressMMapChunker(
		frameSizeU,
		int64(len(compressedData)),
		storage.MemoryChunkSize,
		mockStorage,
		"test/path",
		cachePath,
		testMetrics(t),
	)
	require.NoError(t, err)

	// First read - should fetch from storage
	slice1, err := chunker.Slice(ctx, 0, 1024, frameTable)
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:1024], slice1)

	// Second read of same data - should come from mmap cache, no new fetch
	slice2, err := chunker.Slice(ctx, 0, 1024, frameTable)
	require.NoError(t, err)
	assert.Equal(t, uncompressedData[:1024], slice2)

	// With mmap caching, should only have fetched once
	mockStorage.AssertNumberOfCalls(t, "GetFrame", 1)

	// Verify cache file exists before Close
	_, err = os.Stat(cachePath)
	require.NoError(t, err, "cache file should exist before Close")

	chunker.Close()

	// Cache file is cleaned up on Close
	_, err = os.Stat(cachePath)
	require.Error(t, err, "cache file should be removed after Close")
}

// =============================================================================
// UncompressedMMapChunker Tests
// =============================================================================

func TestUncompressedMMapChunker_CachePersists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dataSize := int64(4 * 1024 * 1024)
	testData := makeTestData(dataSize)

	mockStorage := setupMockStorageUncompressed(t, testData)

	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "cache")

	chunker, err := NewUncompressedMMapChunker(
		dataSize,
		storage.MemoryChunkSize,
		mockStorage,
		"test/path",
		cachePath,
		testMetrics(t),
	)
	require.NoError(t, err)

	// First read - fetches from storage
	slice1, err := chunker.Slice(ctx, 0, 1024, nil)
	require.NoError(t, err)
	assert.Equal(t, testData[:1024], slice1)

	// Second read of same region - should come from cache
	slice2, err := chunker.Slice(ctx, 0, 1024, nil)
	require.NoError(t, err)
	assert.Equal(t, testData[:1024], slice2)

	// Verify cache file exists before Close
	_, err = os.Stat(cachePath)
	require.NoError(t, err, "cache file should exist before Close")

	chunker.Close()

	// Cache file is cleaned up on Close (same behavior as DecompressMMapChunker)
	_, err = os.Stat(cachePath)
	require.Error(t, err, "cache file should be removed after Close")
}

// =============================================================================
// Matrix Tests
// =============================================================================

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

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressed})
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

// --- Matrix runner functions ---

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

// runMatrixChunkHandlesFrameSpan tests that cross-frame reads trigger read spans frame boundary error.
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
	require.Error(t, err, "cross-frame read should error with read spans frame boundary")
	assert.Contains(t, err.Error(), "read spans frame boundary", "error should indicate slow path was triggered")
}

// --- Matrix test runners ---

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

// =============================================================================
// Benchmarks
// =============================================================================

// Benchmark parameters
const (
	benchDataSize   = 1024 * 1024 * 1024 // 1GB total data
	benchPageSize   = 2 * 1024 * 1024    // 2MB hugepage (typical UFFD request)
	benchRandomReqs = 500                // number of random requests
	benchLRUFrames  = 4                  // LRU size as in production
)

// Simulated GCS parameters — used to compute estimated latency from fetch stats,
// no actual sleeping occurs.
const (
	gcsLatency      = 20 * time.Millisecond // first-byte latency per fetch
	gcsBandwidthBps = 400 * 1024 * 1024     // 400 MB/s in bytes/s
)

// benchEnv holds shared test data and configuration for chunker benchmarks.
// All data is stored in memory to avoid disk I/O during benchmark iterations.
type benchEnv struct {
	origData       []byte
	files          map[string][]byte // in-memory file store, shared across iterations
	cPath          string            // key for compressed data in files
	uncompPath     string            // key for uncompressed data in files
	frameTable     *storage.FrameTable
	compressedSize int64
	randomOffsets  []int64
	seqOffsets     []int64
	m              metrics.Metrics
}

func newBenchEnv(b *testing.B) *benchEnv {
	b.Helper()

	origData := generateBenchData(benchDataSize, 42)

	// Compress entirely in memory using StoreReader → memStorage.
	cPath := "data.zst"
	uncompPath := "data.raw"
	files := map[string][]byte{
		uncompPath: origData,
	}

	st, _ := newMemStorage(files)
	frameTable, err := st.StoreReader(
		context.Background(), bytes.NewReader(origData), int64(len(origData)),
		cPath, storage.DefaultCompressionOptions)
	require.NoError(b, err)

	rng := rand.New(rand.NewSource(12345))
	randomOffsets := make([]int64, benchRandomReqs)
	for i := range randomOffsets {
		randomOffsets[i] = int64(rng.Intn(benchDataSize/benchPageSize)) * benchPageSize
	}

	numPages := benchDataSize / benchPageSize
	seqOffsets := make([]int64, numPages)
	for i := range seqOffsets {
		seqOffsets[i] = int64(i) * benchPageSize
	}

	m, _ := metrics.NewMetrics(noop.NewMeterProvider())

	b.Logf("Compression: 1GB -> %dMB (%.2fx), %d frames",
		frameTable.TotalCompressedSize()>>20,
		float64(benchDataSize)/float64(frameTable.TotalCompressedSize()),
		len(frameTable.Frames))

	return &benchEnv{
		origData:       origData,
		files:          files,
		cPath:          cPath,
		uncompPath:     uncompPath,
		frameTable:     frameTable,
		compressedSize: frameTable.TotalCompressedSize(),
		randomOffsets:  randomOffsets,
		seqOffsets:     seqOffsets,
		m:              m,
	}
}

func BenchmarkChunkers(b *testing.B) {
	env := newBenchEnv(b)

	b.Run("UncompressedMMap", func(b *testing.B) {
		b.Run("ColdSequential", func(b *testing.B) {
			benchCold(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewUncompressedMMapChunker(benchDataSize, benchPageSize, st, "data.raw", filepath.Join(cacheDir, "cache"), env.m)
				require.NoError(b, err)

				return c
			}, nil)
		})
		b.Run("RandomAccess", func(b *testing.B) {
			benchRandom(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewUncompressedMMapChunker(benchDataSize, benchPageSize, st, "data.raw", filepath.Join(cacheDir, "cache"), env.m)
				require.NoError(b, err)

				return c
			}, nil)
		})
	})

	b.Run("DecompressMMap", func(b *testing.B) {
		b.Run("ColdSequential", func(b *testing.B) {
			benchCold(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewDecompressMMapChunker(benchDataSize, env.compressedSize, benchPageSize, st, env.cPath, filepath.Join(cacheDir, "cache"), env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
		b.Run("RandomAccess", func(b *testing.B) {
			benchRandom(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewDecompressMMapChunker(benchDataSize, env.compressedSize, benchPageSize, st, env.cPath, filepath.Join(cacheDir, "cache"), env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
	})

	b.Run("CompressLRU", func(b *testing.B) {
		b.Run("ColdSequential", func(b *testing.B) {
			benchCold(b, env, func(_ string, st *storage.Storage) Chunker {
				c, err := NewCompressLRUChunker(benchDataSize, st, env.cPath, benchLRUFrames, env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
		b.Run("RandomAccess", func(b *testing.B) {
			benchRandom(b, env, func(_ string, st *storage.Storage) Chunker {
				c, err := NewCompressLRUChunker(benchDataSize, st, env.cPath, benchLRUFrames, env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
	})

	b.Run("CompressMMapLRU", func(b *testing.B) {
		b.Run("ColdSequential", func(b *testing.B) {
			benchCold(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewCompressMMapLRUChunker(benchDataSize, env.compressedSize, st, env.cPath, filepath.Join(cacheDir, "cache"), benchLRUFrames, env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
		b.Run("RandomAccess", func(b *testing.B) {
			benchRandom(b, env, func(cacheDir string, st *storage.Storage) Chunker {
				c, err := NewCompressMMapLRUChunker(benchDataSize, env.compressedSize, st, env.cPath, filepath.Join(cacheDir, "cache"), benchLRUFrames, env.m)
				require.NoError(b, err)

				return c
			}, env.frameTable)
		})
	})
}

type benchChunkerFactory func(cacheDir string, st *storage.Storage) Chunker

func reportBenchMetrics(b *testing.B, elapsed time.Duration, slices int, ms *memStorage, servedBytes int64) {
	b.Helper()

	fetchedBytes, fetchCalls := ms.stats()

	b.ReportMetric(float64(slices), "slices")
	b.ReportMetric(float64(fetchCalls), "fetches")
	b.ReportMetric(float64(fetchedBytes>>20), "fetched_MB")

	hitRate := 1.0
	if slices > 0 {
		hitRate = 1.0 - float64(fetchCalls)/float64(slices)
	}
	b.ReportMetric(hitRate*100, "hit%")

	if fetchedBytes > 0 {
		b.ReportMetric(math.Round(float64(servedBytes)/float64(fetchedBytes)*100)/100, "amplification")
	}

	// Simulated GCS latency: first-byte latency per fetch + transfer time
	simGCS := time.Duration(fetchCalls) * gcsLatency
	simGCS += time.Duration(float64(fetchedBytes) / float64(gcsBandwidthBps) * float64(time.Second))
	simTotal := elapsed + simGCS
	b.ReportMetric(math.Round(simTotal.Seconds()/float64(slices)*100000)/100, "sim_ms/slice")
	b.ReportMetric(math.Round(simGCS.Seconds()*100)/100, "sim_gcs_s")
}

// benchCold measures cold-start sequential read of entire dataset.
func benchCold(b *testing.B, env *benchEnv, factory benchChunkerFactory, ft *storage.FrameTable) {
	b.Helper()

	ctx := context.Background()
	sliceCount := len(env.seqOffsets)
	servedBytes := int64(sliceCount) * benchPageSize

	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		st, ms := newMemStorage(env.files)
		cacheDir := filepath.Join(b.TempDir(), "cache")
		require.NoError(b, os.MkdirAll(cacheDir, 0o755))
		chunker := factory(cacheDir, st)
		b.StartTimer()

		t0 := time.Now()
		for _, off := range env.seqOffsets {
			_, err := chunker.Slice(ctx, off, benchPageSize, ft)
			require.NoError(b, err)
		}
		elapsed := time.Since(t0)

		b.StopTimer()
		reportBenchMetrics(b, elapsed, sliceCount, ms, servedBytes)
		chunker.Close()
		b.StartTimer()
	}

	b.SetBytes(benchDataSize)
}

// benchRandom measures random access latency with simulated GCS.
func benchRandom(b *testing.B, env *benchEnv, factory benchChunkerFactory, ft *storage.FrameTable) {
	b.Helper()

	ctx := context.Background()
	servedBytes := int64(benchRandomReqs) * benchPageSize

	b.ResetTimer()
	for b.Loop() {
		b.StopTimer()
		st, ms := newMemStorage(env.files)
		cacheDir := filepath.Join(b.TempDir(), "cache")
		require.NoError(b, os.MkdirAll(cacheDir, 0o755))
		chunker := factory(cacheDir, st)
		b.StartTimer()

		t0 := time.Now()
		for _, off := range env.randomOffsets {
			_, err := chunker.Slice(ctx, off, benchPageSize, ft)
			require.NoError(b, err)
		}
		elapsed := time.Since(t0)

		b.StopTimer()
		reportBenchMetrics(b, elapsed, benchRandomReqs, ms, servedBytes)
		chunker.Close()
		b.StartTimer()
	}

	b.SetBytes(servedBytes)
}

func generateBenchData(size int, seed int64) []byte {
	rng := rand.New(rand.NewSource(seed))
	data := make([]byte, size)
	pos := 0
	for pos < size {
		val := byte(rng.Intn(256))
		repeatLen := rng.Intn(32) + 1
		if pos+repeatLen > size {
			repeatLen = size - pos
		}
		for i := range repeatLen {
			data[pos+i] = val
		}
		pos += repeatLen
	}

	return data
}

// =============================================================================
// In-memory storage backend (benchmarks read from memory, no disk I/O)
// =============================================================================

// memStorage is an in-memory storage.Backend that tracks read statistics.
type memStorage struct {
	files     map[string][]byte // shared read-only data
	readBytes atomic.Int64
	readCalls atomic.Int64
}

func (m *memStorage) RangeGet(_ context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	m.readCalls.Add(1)
	m.readBytes.Add(int64(length))

	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("memStorage: %s not found", path)
	}

	end := min(offset+int64(length), int64(len(data)))

	return io.NopCloser(bytes.NewReader(data[offset:end])), nil
}

func (m *memStorage) StartDownload(_ context.Context, path string) (io.ReadCloser, error) {
	data, ok := m.files[path]
	if !ok {
		return nil, fmt.Errorf("memStorage: %s not found", path)
	}

	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *memStorage) Upload(_ context.Context, _ string, _ io.Reader) (int64, error) {
	return 0, fmt.Errorf("memStorage: not implemented")
}

func (m *memStorage) Size(_ context.Context, path string) (int64, int64, error) {
	data, ok := m.files[path]
	if !ok {
		return 0, 0, fmt.Errorf("memStorage: %s not found", path)
	}

	return int64(len(data)), int64(len(data)), nil
}

func (m *memStorage) DeleteWithPrefix(_ context.Context, _ string) error { return nil }
func (m *memStorage) String() string                                     { return "memStorage" }

func (m *memStorage) MakeMultipartUpload(_ context.Context, objectPath string, _ storage.RetryConfig, _ map[string]string) (storage.MultipartUploader, func(), int, error) {
	return &memUploader{ms: m, path: objectPath}, func() {}, 50 * 1024 * 1024, nil
}

func (m *memStorage) stats() (bytesRead, calls int64) {
	return m.readBytes.Load(), m.readCalls.Load()
}

// memUploader collects multipart upload parts in memory.
type memUploader struct {
	ms    *memStorage
	path  string
	mu    sync.Mutex
	parts map[int][]byte
}

func (u *memUploader) Start(_ context.Context) error {
	u.parts = make(map[int][]byte)

	return nil
}

func (u *memUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	var buf []byte
	for _, d := range data {
		buf = append(buf, d...)
	}
	u.parts[partIndex] = buf

	return nil
}

func (u *memUploader) Complete(_ context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	var result []byte
	// Parts are 1-indexed (frame encoder pre-increments before UploadPart).
	for i := 1; i <= len(u.parts); i++ {
		result = append(result, u.parts[i]...)
	}
	u.ms.files[u.path] = result

	return nil
}

// newMemStorage creates a *storage.Storage backed by in-memory data,
// with RangeGet instrumented to track fetch statistics.
func newMemStorage(files map[string][]byte) (*storage.Storage, *memStorage) {
	ms := &memStorage{files: files}

	return &storage.Storage{
		Backend: &storage.Backend{
			Basic:                    ms,
			RangeGetter:              ms,
			Manager:                  ms,
			MultipartUploaderFactory: ms,
		},
	}, ms
}

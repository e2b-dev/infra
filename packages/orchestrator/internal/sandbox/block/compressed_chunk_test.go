package block

import (
	"context"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

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

func TestCompressedChunker_BasicReadAt(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data - one frame of 8MB uncompressed (2 chunks)
	frameSizeU := int64(8 * 1024 * 1024) // 8MB
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

	chunker, err := NewCompressLRUChunker(
		frameSizeU, // virtSize
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read the first 1024 bytes
	buf := make([]byte, 1024)
	n, err := chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, uncompressedData[:1024], buf)

	// Should have called the getter once
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// Read again from LRU - should not call getter again
	n, err = chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)
}

func TestCompressedChunker_LRUPopulation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data - one frame of 8MB (2 chunks)
	frameSizeU := int64(8 * 1024 * 1024)
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
	buf := make([]byte, 100)
	_, err = chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)

	// One frame should be in LRU
	lruCount, _ := chunker.LRUStats()
	assert.Equal(t, 1, lruCount)

	// Reading from another part of the same frame should not trigger another fetch
	_, err = chunker.ReadAt(ctx, buf, storage.MemoryChunkSize, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)
}

func TestCompressedChunker_LRUEvictionRefetch(t *testing.T) {
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
	buf := make([]byte, 100)
	_, err = chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)

	// LRU should have the frame
	lruCount, _ := chunker.LRUStats()
	assert.Equal(t, 1, lruCount)

	// Purge LRU to simulate eviction
	chunker.frameLRU.Purge()

	// Read again - must re-fetch from storage (NFS cache would handle file caching in production)
	_, err = chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2) // Re-fetched after LRU eviction
}

func TestCompressedChunker_SliceAcrossChunks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data spanning multiple chunks
	frameSizeU := int64(8 * 1024 * 1024) // 8MB = 2 chunks
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

func TestCompressedChunker_MultipleFrames(t *testing.T) {
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
	buf := make([]byte, 100)
	_, err = chunker.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data1[:100], buf)

	// Read from second frame
	_, err = chunker.ReadAt(ctx, buf, frameSizeU, frameTable)
	require.NoError(t, err)
	assert.Equal(t, data2[:100], buf)

	// Both frames should have been fetched
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 2)
}

func TestCompressedChunker_SliceAcrossFrames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024) // 4MB per frame
	totalSize := frameSizeU * 3          // 3 frames

	// Create data for three frames with distinct patterns
	data1 := make([]byte, frameSizeU)
	data2 := make([]byte, frameSizeU)
	data3 := make([]byte, frameSizeU)
	for i := range data1 {
		data1[i] = byte(i % 256)
		data2[i] = byte((i + 100) % 256)
		data3[i] = byte((i + 200) % 256)
	}

	// Combine for verification
	allData := make([]byte, totalSize)
	copy(allData[0:], data1)
	copy(allData[frameSizeU:], data2)
	copy(allData[frameSizeU*2:], data3)

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

	chunker, err := NewCompressLRUChunker(
		totalSize,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Read spanning frames 1 and 2
	offset := frameSizeU - 500
	length := int64(1000) // spans 500 bytes into frame 2
	slice, err := chunker.Slice(ctx, offset, length, frameTable)
	require.NoError(t, err)
	assert.Equal(t, allData[offset:offset+length], slice)

	// Read spanning all three frames
	offset = frameSizeU - 100
	length = frameSizeU + 200 // 100 from frame 1, all of frame 2, 100 from frame 3
	slice, err = chunker.Slice(ctx, offset, length, frameTable)
	require.NoError(t, err)
	assert.Equal(t, allData[offset:offset+length], slice)

	// All 3 frames should have been fetched
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 3)
}

func TestCompressedChunker_ConcurrentReads(t *testing.T) {
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

	chunker, err := NewCompressLRUChunker(
		frameSizeU,
		mockGetter,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Concurrent reads
	const numReaders = 10
	var wg sync.WaitGroup
	wg.Add(numReaders)

	for range numReaders {
		go func() {
			defer wg.Done()
			buf := make([]byte, 100)
			_, err := chunker.ReadAt(ctx, buf, 0, frameTable)
			assert.NoError(t, err)
		}()
	}

	wg.Wait()

	// Should only fetch once despite concurrent readers (WaitMap deduplication)
	mockGetter.AssertNumberOfCalls(t, "GetFrame", 1)
}

func TestCompressedChunker_EmptySlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	frameSizeU := int64(4 * 1024 * 1024)
	uncompressedData := make([]byte, frameSizeU)
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

	// Empty slice should return empty
	slice, err := chunker.Slice(ctx, 0, 0, frameTable)
	require.NoError(t, err)
	assert.Empty(t, slice)
}

func TestCompressedChunker_Close(t *testing.T) {
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

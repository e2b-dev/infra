package build

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// compressTestBytes compresses data using zstd
func compressTestBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
	require.NoError(t, err)
	defer enc.Close()

	return enc.EncodeAll(data, nil)
}

func testBlockMetrics(t *testing.T) blockmetrics.Metrics {
	t.Helper()
	m, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)

	return m
}

// setupMockProvider creates a mock storage provider that returns compressed frames
func setupMockProvider(t *testing.T, frames map[int64][]byte, frameTable *storage.FrameTable) *storage.MockStorageProvider {
	t.Helper()
	provider := storage.NewMockStorageProvider(t)

	// Setup GetFrame to return compressed data and decompress when requested
	provider.EXPECT().GetFrame(
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(ctx context.Context, objectPath string, offsetU int64, ft *storage.FrameTable, decompress bool, buf []byte) (storage.Range, error) {
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

			// Use DecodeAll to decompress all data at once
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

	// Size is used for uncompressed data
	provider.EXPECT().Size(mock.Anything, mock.Anything).Return(
		frameTable.StartAt.U+frameTable.TotalUncompressedSize(), nil,
	).Maybe()

	return provider
}

// TestStorageDiff_CompressedChunker_ReadVerify verifies that the compressed chunker
// reads data correctly
func TestStorageDiff_CompressedChunker_ReadVerify(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create test data - 4MB (1 chunk)
	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
		frameTable,
		WithChunkerType(ChunkerTypeCompressed),
	)
	require.NoError(t, err)

	err = sd.Init(ctx)
	require.NoError(t, err)
	defer sd.Close()

	// Read from start
	buf := make([]byte, 1024)
	n, err := sd.ReadAt(ctx, buf, 0)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[:1024], buf, "Start data doesn't match")

	// Read from middle
	n, err = sd.ReadAt(ctx, buf, frameSizeU/2)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[frameSizeU/2:frameSizeU/2+1024], buf, "Middle data doesn't match")

	// Read near end
	n, err = sd.ReadAt(ctx, buf, frameSizeU-1024)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[frameSizeU-1024:], buf, "End data doesn't match")

	// Slice
	slice, err := sd.Slice(ctx, 100, 500)
	require.NoError(t, err)
	assert.Equal(t, testData[100:600], slice, "Slice data doesn't match")
}

// TestStorageDiff_CompressedChunker_Init verifies that the compressed chunker
// initializes correctly via StorageDiff
func TestStorageDiff_CompressedChunker_Init(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-compressed",
		Memfile,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
		frameTable,
		WithChunkerType(ChunkerTypeCompressed),
		WithLRUSize(128),
	)
	require.NoError(t, err)

	err = sd.Init(ctx)
	require.NoError(t, err)
	defer sd.Close()

	// Verify we can read data
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, testData[:100], buf)
}

// TestStorageDiff_MmapChunker_Init verifies that the mmap chunker
// initializes correctly via StorageDiff (default behavior)
func TestStorageDiff_MmapChunker_Init(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

	// Default chunker type (no option) should be Mmap
	sd, err := newStorageDiff(
		tmpDir,
		"test-build-mmap",
		Memfile,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
		frameTable,
	)
	require.NoError(t, err)
	assert.Equal(t, ChunkerTypeMmap, sd.chunkerType)

	err = sd.Init(ctx)
	require.NoError(t, err)
	defer sd.Close()

	// Verify we can read data
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, testData[:100], buf)
}

// TestStorageDiff_FileSize verifies FileSize returns reasonable values
func TestStorageDiff_FileSize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	tests := []struct {
		name        string
		chunkerType ChunkerType
	}{
		{"Mmap", ChunkerTypeMmap},
		{"Compressed", ChunkerTypeCompressed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testDir := filepath.Join(tmpDir, tc.name)
			err := os.MkdirAll(testDir, 0o755)
			require.NoError(t, err)

			provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

			sd, err := newStorageDiff(
				testDir,
				"test-build-filesize",
				Rootfs,
				int64(storage.MemoryChunkSize),
				testBlockMetrics(t),
				provider,
				frameTable,
				WithChunkerType(tc.chunkerType),
			)
			require.NoError(t, err)

			err = sd.Init(ctx)
			require.NoError(t, err)
			defer sd.Close()

			// Read some data to populate cache
			buf := make([]byte, 100)
			_, err = sd.ReadAt(ctx, buf, 0)
			require.NoError(t, err)

			// FileSize should return something reasonable
			size, err := sd.FileSize()
			require.NoError(t, err)
			// Both should be >= 0
			assert.GreaterOrEqual(t, size, int64(0))
		})
	}
}

// TestStorageDiff_Close verifies Close works correctly
func TestStorageDiff_Close(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	tests := []struct {
		name        string
		chunkerType ChunkerType
	}{
		{"Mmap", ChunkerTypeMmap},
		{"Compressed", ChunkerTypeCompressed},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testDir := filepath.Join(tmpDir, tc.name)
			err := os.MkdirAll(testDir, 0o755)
			require.NoError(t, err)

			provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

			sd, err := newStorageDiff(
				testDir,
				"test-build-close",
				Rootfs,
				int64(storage.MemoryChunkSize),
				testBlockMetrics(t),
				provider,
				frameTable,
				WithChunkerType(tc.chunkerType),
			)
			require.NoError(t, err)

			err = sd.Init(ctx)
			require.NoError(t, err)

			// Close should not error
			err = sd.Close()
			require.NoError(t, err)
		})
	}
}

// TestStorageDiff_CompressedChunker_ConcurrentReads verifies concurrent reads work
// correctly for the compressed chunker
func TestStorageDiff_CompressedChunker_ConcurrentReads(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}
	compressedData := compressTestBytes(t, testData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressedData))},
		},
	}

	provider := setupMockProvider(t, map[int64][]byte{0: compressedData}, frameTable)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-concurrent",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
		frameTable,
		WithChunkerType(ChunkerTypeCompressed),
	)
	require.NoError(t, err)

	err = sd.Init(ctx)
	require.NoError(t, err)
	defer sd.Close()

	// Run concurrent reads
	const numReaders = 10
	var wg sync.WaitGroup
	errors := make(chan error, numReaders)

	for i := range numReaders {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()
			buf := make([]byte, 100)
			n, err := sd.ReadAt(ctx, buf, offset)
			if err != nil {
				errors <- err

				return
			}
			if n != 100 {
				errors <- assert.AnError

				return
			}
			// Verify data
			expected := testData[offset : offset+100]
			if !bytes.Equal(buf, expected) {
				errors <- assert.AnError
			}
		}(int64(i * 1000))
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		require.NoError(t, err)
	}
}

// TestStorageDiff_CompressedChunker_MultipleFrames verifies reading across multiple frames
// with the compressed chunker
func TestStorageDiff_CompressedChunker_MultipleFrames(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024) // 4MB per frame

	// Create data for two frames
	data1 := make([]byte, frameSizeU)
	data2 := make([]byte, frameSizeU)
	for i := range data1 {
		data1[i] = byte(i % 256)
		data2[i] = byte((i + 100) % 256)
	}
	fullData := append(data1, data2...)

	compressed1 := compressTestBytes(t, data1)
	compressed2 := compressTestBytes(t, data2)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(len(compressed1))},
			{U: int32(frameSizeU), C: int32(len(compressed2))},
		},
	}

	provider := setupMockProvider(t, map[int64][]byte{
		0:          compressed1,
		frameSizeU: compressed2,
	}, frameTable)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-multiframe",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
		frameTable,
		WithChunkerType(ChunkerTypeCompressed),
	)
	require.NoError(t, err)

	err = sd.Init(ctx)
	require.NoError(t, err)
	defer sd.Close()

	// Read from first frame
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, fullData[:100], buf)

	// Read from second frame
	n, err = sd.ReadAt(ctx, buf, frameSizeU+1000)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, fullData[frameSizeU+1000:frameSizeU+1100], buf)

	// Read across frame boundary
	boundaryOffset := frameSizeU - 50
	buf = make([]byte, 100)
	n, err = sd.ReadAt(ctx, buf, boundaryOffset)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, fullData[boundaryOffset:boundaryOffset+100], buf)

	// Verify Size
	size, err := sd.Size(ctx)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, int64(0))
}

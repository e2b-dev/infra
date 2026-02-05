package build

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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

// setupMockProvider creates a mock storage provider that returns compressed frames and headers
func setupMockProvider(t *testing.T, buildId string, diffType DiffType, frames map[int64][]byte, frameTable *storage.FrameTable, dataSize int64) *storage.MockStorageProvider {
	t.Helper()
	provider := storage.NewMockStorageProvider(t)

	// Create and serialize a test header
	// Use a deterministic UUID based on buildId for test reproducibility
	bid, err := uuid.Parse(buildId)
	if err != nil {
		// buildId is not a valid UUID, generate one from it
		bid = uuid.NewSHA1(uuid.NameSpaceOID, []byte(buildId))
	}

	metadata := &header.Metadata{
		Version:     4, // Version 4 required for frame table serialization
		BuildId:     bid,
		BaseBuildId: bid,
		Size:        uint64(dataSize),
		BlockSize:   uint64(storage.MemoryChunkSize),
		Generation:  1,
	}

	h, err := header.NewHeader(metadata, nil)
	require.NoError(t, err)

	// Add frame table to header if provided
	if frameTable != nil {
		err = h.AddFrames(frameTable)
		require.NoError(t, err)
	}

	headerData, err := header.Serialize(h.Metadata, h.Mapping)
	require.NoError(t, err)

	// Mock GetBlob for header fetch (not used with lazy init, but kept for compatibility)
	headerPath := buildId + "/" + string(diffType) + storage.HeaderSuffix
	provider.EXPECT().GetBlob(
		mock.Anything,
		headerPath,
	).Return(headerData, nil).Maybe()

	// Setup GetFrame to return data, decompressing only if data is actually compressed
	provider.EXPECT().GetFrame(
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
		mock.Anything,
	).RunAndReturn(func(_ context.Context, _ string, offsetU int64, ft *storage.FrameTable, decompress bool, buf []byte) (storage.Range, error) {
		data, ok := frames[offsetU]
		if !ok {
			return storage.Range{}, nil
		}

		// Only decompress if the data is actually compressed (check frame table)
		isCompressed := ft != nil && ft.CompressionType == storage.CompressionZstd
		if decompress && isCompressed {
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

	// Size returns (virtSize, rawSize) - virtSize is uncompressed, rawSize is compressed
	// Calculate rawSize from the compressed frames
	var rawSize int64
	for _, data := range frames {
		rawSize += int64(len(data))
	}
	provider.EXPECT().Size(mock.Anything, mock.Anything).Return(dataSize, rawSize, nil).Maybe()

	return provider
}

// TestStorageDiff_CompressedChunker_ReadVerify verifies that the compressed chunker
// reads data correctly with lazy initialization
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

	provider := setupMockProvider(t, "test-build", Rootfs, map[int64][]byte{0: compressedData}, frameTable, frameSizeU)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)
	defer sd.Close()

	// Read from start - chunker lazily initialized on first read
	buf := make([]byte, 1024)
	n, err := sd.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[:1024], buf, "Start data doesn't match")

	// Read from middle
	n, err = sd.ReadAt(ctx, buf, frameSizeU/2, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[frameSizeU/2:frameSizeU/2+1024], buf, "Middle data doesn't match")

	// Read near end
	n, err = sd.ReadAt(ctx, buf, frameSizeU-1024, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 1024, n)
	assert.Equal(t, testData[frameSizeU-1024:], buf, "End data doesn't match")

	// Slice
	slice, err := sd.Slice(ctx, 100, 500, frameTable)
	require.NoError(t, err)
	assert.Equal(t, testData[100:600], slice, "Slice data doesn't match")
}

// TestStorageDiff_CompressedChunker_LazyInit verifies that the compressed chunker
// initializes lazily on first read via StorageDiff
func TestStorageDiff_CompressedChunker_LazyInit(t *testing.T) {
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

	provider := setupMockProvider(t, "test-build-compressed", Memfile, map[int64][]byte{0: compressedData}, frameTable, frameSizeU)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-compressed",
		Memfile,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)
	defer sd.Close()

	// Verify compressed data is detected and read correctly - chunker created lazily
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, testData[:100], buf)
}

// TestStorageDiff_MmapChunker_LazyInit verifies that the mmap chunker
// initializes lazily on first read via StorageDiff (uncompressed data)
func TestStorageDiff_MmapChunker_LazyInit(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// Mmap chunker requires uncompressed data (nil or CompressionNone frame table)
	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionNone,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(frameSizeU), C: int32(frameSizeU)},
		},
	}

	provider := setupMockProvider(t, "test-build-mmap", Memfile, map[int64][]byte{0: testData}, frameTable, frameSizeU)

	// Uncompressed data should use Mmap chunker
	sd, err := newStorageDiff(
		tmpDir,
		"test-build-mmap",
		Memfile,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)
	defer sd.Close()

	// Verify we can read data - chunker created lazily based on frame table
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0, frameTable)
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

	tests := []struct {
		name       string
		frameTable *storage.FrameTable
		mockData   []byte
	}{
		{
			name: "Mmap",
			frameTable: &storage.FrameTable{
				CompressionType: storage.CompressionNone,
				StartAt:         storage.FrameOffset{U: 0, C: 0},
				Frames:          []storage.FrameSize{{U: int32(frameSizeU), C: int32(frameSizeU)}},
			},
			mockData: testData,
		},
		{
			name: "Compressed",
			frameTable: &storage.FrameTable{
				CompressionType: storage.CompressionZstd,
				StartAt:         storage.FrameOffset{U: 0, C: 0},
				Frames:          []storage.FrameSize{{U: int32(frameSizeU), C: int32(len(compressedData))}},
			},
			mockData: compressedData,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testDir := filepath.Join(tmpDir, tc.name)
			err := os.MkdirAll(testDir, 0o755)
			require.NoError(t, err)

			buildId := "test-build-filesize-" + tc.name
			provider := setupMockProvider(t, buildId, Rootfs, map[int64][]byte{0: tc.mockData}, tc.frameTable, frameSizeU)

			sd, err := newStorageDiff(
				testDir,
				buildId,
				Rootfs,
				int64(storage.MemoryChunkSize),
				testBlockMetrics(t),
				provider,
			)
			require.NoError(t, err)
			defer sd.Close()

			// Read some data to populate cache and initialize chunker
			buf := make([]byte, 100)
			_, err = sd.ReadAt(ctx, buf, 0, tc.frameTable)
			require.NoError(t, err)

			// FileSize should return something reasonable after chunker is initialized
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

	tests := []struct {
		name       string
		frameTable *storage.FrameTable
		mockData   []byte
	}{
		{
			name: "Mmap",
			frameTable: &storage.FrameTable{
				CompressionType: storage.CompressionNone,
				StartAt:         storage.FrameOffset{U: 0, C: 0},
				Frames:          []storage.FrameSize{{U: int32(frameSizeU), C: int32(frameSizeU)}},
			},
			mockData: testData,
		},
		{
			name: "Compressed",
			frameTable: &storage.FrameTable{
				CompressionType: storage.CompressionZstd,
				StartAt:         storage.FrameOffset{U: 0, C: 0},
				Frames:          []storage.FrameSize{{U: int32(frameSizeU), C: int32(len(compressedData))}},
			},
			mockData: compressedData,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testDir := filepath.Join(tmpDir, tc.name)
			err := os.MkdirAll(testDir, 0o755)
			require.NoError(t, err)

			buildId := "test-build-close-" + tc.name
			provider := setupMockProvider(t, buildId, Rootfs, map[int64][]byte{0: tc.mockData}, tc.frameTable, frameSizeU)

			sd, err := newStorageDiff(
				testDir,
				buildId,
				Rootfs,
				int64(storage.MemoryChunkSize),
				testBlockMetrics(t),
				provider,
			)
			require.NoError(t, err)

			// Initialize chunker by reading
			buf := make([]byte, 100)
			_, err = sd.ReadAt(ctx, buf, 0, tc.frameTable)
			require.NoError(t, err)

			// Close should not error
			err = sd.Close()
			require.NoError(t, err)
		})
	}
}

// TestStorageDiff_CloseWithoutInit verifies Close works even if chunker was never initialized
func TestStorageDiff_CloseWithoutInit(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	frameSizeU := int64(4 * 1024 * 1024)
	testData := make([]byte, frameSizeU)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionNone,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames:          []storage.FrameSize{{U: int32(frameSizeU), C: int32(frameSizeU)}},
	}

	provider := setupMockProvider(t, "test-build-close-noinit", Rootfs, map[int64][]byte{0: testData}, frameTable, frameSizeU)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-close-noinit",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)

	// Close without ever reading (chunker never initialized)
	err = sd.Close()
	require.NoError(t, err)
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

	provider := setupMockProvider(t, "test-build-concurrent", Rootfs, map[int64][]byte{0: compressedData}, frameTable, frameSizeU)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-concurrent",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)
	defer sd.Close()

	// Run concurrent reads - chunker will be lazily initialized on first read
	const numReaders = 10
	var wg sync.WaitGroup
	errors := make(chan error, numReaders)

	for i := range numReaders {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()
			buf := make([]byte, 100)
			n, err := sd.ReadAt(ctx, buf, offset, frameTable)
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

// TestStorageDiff_CompressedChunker_MultipleFrames verifies reading from multiple frames
// with the compressed chunker. Cross-frame reads are now rejected by the Chunk interface.
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
	fullData := append([]byte{}, data1...)
	fullData = append(fullData, data2...)

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

	provider := setupMockProvider(t, "test-build-multiframe", Rootfs, map[int64][]byte{
		0:          compressed1,
		frameSizeU: compressed2,
	}, frameTable, frameSizeU*2)

	sd, err := newStorageDiff(
		tmpDir,
		"test-build-multiframe",
		Rootfs,
		int64(storage.MemoryChunkSize),
		testBlockMetrics(t),
		provider,
	)
	require.NoError(t, err)
	defer sd.Close()

	// Read from first frame - chunker lazily initialized
	buf := make([]byte, 100)
	n, err := sd.ReadAt(ctx, buf, 0, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, fullData[:100], buf)

	// Read from second frame
	n, err = sd.ReadAt(ctx, buf, frameSizeU+1000, frameTable)
	require.NoError(t, err)
	assert.Equal(t, 100, n)
	assert.Equal(t, fullData[frameSizeU+1000:frameSizeU+1100], buf)

	// Read across frame boundary - should trigger SLOW_PATH_HIT error
	// This verifies whether cross-frame reads ever happen in practice
	boundaryOffset := frameSizeU - 50
	buf = make([]byte, 100)
	_, err = sd.ReadAt(ctx, buf, boundaryOffset, frameTable)
	require.Error(t, err, "cross-frame reads should trigger SLOW_PATH_HIT error")
	assert.Contains(t, err.Error(), "SLOW_PATH_HIT", "error should indicate slow path was triggered")

	// Verify FileSize after chunker is initialized
	size, err := sd.FileSize()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, size, int64(0))
}

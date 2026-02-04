package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

// compressTestData compresses data using zstd and returns the compressed bytes.
func compressTestData(t *testing.T, data []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	require.NoError(t, err)
	defer enc.Close()

	return enc.EncodeAll(data, nil)
}

// makeTestData creates test data with a simple pattern.
func makeTestData(size int, seed byte) []byte {
	data := make([]byte, size)
	for i := range data {
		data[i] = byte((int(seed) + i) % 256)
	}

	return data
}

// makeRepetitiveData creates highly compressible test data.
func makeRepetitiveData(size int) []byte {
	data := make([]byte, size)
	pattern := []byte("ABCDEFGHIJ")
	for i := range data {
		data[i] = pattern[i%len(pattern)]
	}

	return data
}

// newTestCache creates a Cache for testing with sensible defaults.
func newTestCache(t *testing.T, inner StorageProvider, chunkSize int64) *Cache {
	t.Helper()

	return &Cache{
		rootPath:  t.TempDir(),
		inner:     inner,
		chunkSize: chunkSize,
		tracer:    noopTracer,
	}
}

// newTestCacheWithFlags creates a Cache for testing with feature flags mock.
func newTestCacheWithFlags(t *testing.T, inner StorageProvider, chunkSize int64) (*Cache, *MockFeatureFlagsClient) {
	t.Helper()
	flags := NewMockFeatureFlagsClient(t)

	return &Cache{
		rootPath:  t.TempDir(),
		inner:     inner,
		chunkSize: chunkSize,
		flags:     flags,
		tracer:    noopTracer,
	}, flags
}

// writeToCache writes data to a cache file path, creating directories as needed.
func writeToCache(t *testing.T, path string, data []byte) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, data, 0o644))
}

// waitForCacheFile waits for a cache file to exist and for all async operations
// (lock files, temp files) to be cleaned up in the same directory.
func waitForCacheFile(t *testing.T, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		_, err := os.Stat(path)

		return err == nil
	}, time.Second, time.Millisecond, "cache file should be written: %s", path)

	// Also wait for any async cache operations to complete in the same directory
	waitForAsyncCacheOps(t, filepath.Dir(path))
}

// singleFrameTable creates a FrameTable with one frame.
func singleFrameTable(compressionType CompressionType, uncompressedSize, compressedSize int) *FrameTable {
	return &FrameTable{
		CompressionType: compressionType,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          []FrameSize{{U: int32(uncompressedSize), C: int32(compressedSize)}},
	}
}

// =============================================================================
// Filename Generation Tests
// =============================================================================

func TestCache_makeChunkFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		rootPath  string
		chunkSize int64
		object    string
		offset    int64
		expected  string
	}{
		{
			name:      "basic",
			rootPath:  "/cache",
			chunkSize: 1024,
			object:    "obj",
			offset:    0,
			expected:  "/cache/obj/000000000000-1024.bin",
		},
		{
			name:      "nested path",
			rootPath:  "/cache",
			chunkSize: 1024,
			object:    "a/b/c",
			offset:    4096,
			expected:  "/cache/a/b/c/000000000004-1024.bin",
		},
		{
			name:      "large offset",
			rootPath:  "/data/cache",
			chunkSize: 4096,
			object:    "file.bin",
			offset:    4096 * 1000,
			expected:  "/data/cache/file.bin/000000001000-4096.bin",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Cache{rootPath: tc.rootPath, chunkSize: tc.chunkSize}
			assert.Equal(t, tc.expected, c.makeChunkFilename(tc.object, tc.offset))
		})
	}
}

func TestCache_makeFrameFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		rootPath string
		object   string
		offset   FrameOffset
		size     FrameSize
		expected string
	}{
		{
			name:     "zero offset",
			rootPath: "/cache",
			object:   "obj",
			offset:   FrameOffset{U: 0, C: 0},
			size:     FrameSize{U: 4096, C: 1024},
			expected: "/cache/obj/0000000000000000C-1024C.frm",
		},
		{
			name:     "non-zero compressed offset",
			rootPath: "/cache",
			object:   "a/b/c",
			offset:   FrameOffset{U: 8192, C: 2048},
			size:     FrameSize{U: 4096, C: 1500},
			expected: "/cache/a/b/c/0000000000002048C-1500C.frm",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Cache{rootPath: tc.rootPath}
			assert.Equal(t, tc.expected, c.makeFrameFilename(tc.object, tc.offset, tc.size))
		})
	}
}

func TestCache_sizeFilename(t *testing.T) {
	t.Parallel()

	c := Cache{rootPath: "/cache"}
	assert.Equal(t, "/cache/obj/size.txt", c.sizeFilename("obj"))
	assert.Equal(t, "/cache/a/b/c/size.txt", c.sizeFilename("a/b/c"))
}

// =============================================================================
// Validation Tests
// =============================================================================

func TestCache_validateReadAtParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		chunkSize  int64
		bufferSize int64
		offset     int64
		wantErr    error
	}{
		{"valid aligned", 1024, 1024, 0, nil},
		{"valid smaller buffer", 1024, 512, 0, nil},
		{"valid non-zero offset", 1024, 1024, 2048, nil},
		{"empty buffer", 1024, 0, 0, ErrBufferTooSmall},
		{"buffer too large", 1024, 2048, 0, ErrBufferTooLarge},
		{"unaligned offset", 1024, 1024, 100, ErrOffsetUnaligned},
		{"crosses chunk boundary", 1024, 1024, 512, ErrOffsetUnaligned},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Cache{chunkSize: tc.chunkSize}
			err := c.validateReadAtParams(tc.bufferSize, tc.offset)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

func TestCache_validateGetFrameParams(t *testing.T) {
	t.Parallel()

	compressedFT := &FrameTable{CompressionType: CompressionZstd}
	uncompressedFT := &FrameTable{CompressionType: CompressionNone}

	tests := []struct {
		name       string
		chunkSize  int64
		offset     int64
		length     int
		frameTable *FrameTable
		wantErr    error
	}{
		{"compressed allows large buffer", 4096, 0, 8192, compressedFT, nil},
		{"compressed allows small buffer", 4096, 0, 100, compressedFT, nil},
		{"uncompressed rejects large buffer", 4096, 0, 8192, uncompressedFT, ErrBufferTooLarge},
		{"uncompressed allows exact chunk", 4096, 0, 4096, uncompressedFT, nil},
		{"empty buffer rejected", 4096, 0, 0, compressedFT, ErrBufferTooSmall},
		{"unaligned offset rejected", 4096, 100, 1024, compressedFT, ErrOffsetUnaligned},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Cache{chunkSize: tc.chunkSize}
			err := c.validateGetFrameParams(tc.offset, tc.length, tc.frameTable, true)
			if tc.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
}

// =============================================================================
// Decompression Tests
// =============================================================================

func TestDecompressBytes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"small repetitive", makeRepetitiveData(256)},
		{"medium sequential", makeTestData(4096, 0)},
		{"large", makeTestData(64*1024, 42)},
		{"zeros", make([]byte, 1024)},
		{"single byte", []byte{0x42}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compressed := compressTestData(t, tc.data)
			buf := make([]byte, len(tc.data))

			n, err := decompressBytes(context.Background(), CompressionZstd, compressed, buf)

			require.NoError(t, err)
			require.Equal(t, len(tc.data), n)
			require.Equal(t, tc.data, buf)
		})
	}
}

func TestDecompressBytes_Errors(t *testing.T) {
	t.Parallel()

	t.Run("unsupported compression", func(t *testing.T) {
		t.Parallel()
		_, err := decompressBytes(context.Background(), CompressionLZ4, []byte{}, make([]byte, 10))
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported")
	})

	t.Run("invalid compressed data", func(t *testing.T) {
		t.Parallel()
		_, err := decompressBytes(context.Background(), CompressionZstd, []byte("not valid zstd"), make([]byte, 100))
		require.Error(t, err)
	})
}

func TestDecompressStream(t *testing.T) {
	t.Parallel()

	origData := makeTestData(8192, 0)
	compressed := compressTestData(t, origData)

	buf := make([]byte, len(origData))
	n, err := decompressStream(context.Background(), CompressionZstd, bytes.NewReader(compressed), buf)

	require.NoError(t, err)
	require.Equal(t, len(origData), n)
	require.Equal(t, origData, buf)
}

func TestDecompressStream_UnsupportedCompression(t *testing.T) {
	t.Parallel()

	_, err := decompressStream(context.Background(), CompressionLZ4, bytes.NewReader([]byte{}), make([]byte, 10))
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported")
}

// =============================================================================
// Size Caching Tests
// =============================================================================

func TestCache_Size_CacheMiss(t *testing.T) {
	t.Parallel()

	inner := NewMockStorageProvider(t)
	inner.EXPECT().Size(mock.Anything, "obj").Return(int64(12345), int64(12345), nil).Once()

	c := newTestCache(t, inner, 1024)

	virtSize, rawSize, err := c.Size(t.Context(), "obj")

	require.NoError(t, err)
	assert.Equal(t, int64(12345), virtSize)
	assert.Equal(t, int64(12345), rawSize)

	// Verify size was cached
	waitForCacheFile(t, c.sizeFilename("obj"))

	// Second call should hit cache (mock not called again since .Once())
	virtSize, rawSize, err = c.Size(t.Context(), "obj")
	require.NoError(t, err)
	assert.Equal(t, int64(12345), virtSize)
	assert.Equal(t, int64(12345), rawSize)
}

func TestCache_Size_CacheHit(t *testing.T) {
	t.Parallel()

	c := newTestCache(t, nil, 1024) // nil inner - should not be called

	// Pre-populate cache with "virtSize rawSize" format
	sizeFile := c.sizeFilename("obj")
	writeToCache(t, sizeFile, []byte("9876 5432"))

	virtSize, rawSize, err := c.Size(t.Context(), "obj")

	require.NoError(t, err)
	assert.Equal(t, int64(9876), virtSize)
	assert.Equal(t, int64(5432), rawSize)
}

func TestCache_Size_CompressedFile(t *testing.T) {
	t.Parallel()

	inner := NewMockStorageProvider(t)
	// Compressed file: virtSize (100000) > rawSize (50000)
	inner.EXPECT().Size(mock.Anything, "obj").Return(int64(100000), int64(50000), nil).Once()

	c := newTestCache(t, inner, 1024)

	virtSize, rawSize, err := c.Size(t.Context(), "obj")

	require.NoError(t, err)
	assert.Equal(t, int64(100000), virtSize)
	assert.Equal(t, int64(50000), rawSize)
}

func TestCache_Size_InnerError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("storage unavailable")
	inner := NewMockStorageProvider(t)
	inner.EXPECT().Size(mock.Anything, "obj").Return(int64(0), int64(0), expectedErr)

	c := newTestCache(t, inner, 1024)

	_, _, err := c.Size(t.Context(), "obj")

	require.ErrorIs(t, err, expectedErr)
}

// waitForAsyncCacheOps waits until no .lock or temp files exist in the given directory.
// It first sleeps briefly to allow any pending goroutines to start.
func waitForAsyncCacheOps(t *testing.T, dir string) {
	t.Helper()
	// Brief sleep to ensure any pending goroutines have started and created their temp/lock files
	time.Sleep(10 * time.Millisecond)
	require.Eventually(t, func() bool {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return true // directory doesn't exist or can't be read
		}
		for _, entry := range entries {
			name := entry.Name()
			// Check for lock files and temp files
			if filepath.Ext(name) == ".lock" || strings.HasPrefix(name, ".temp.") || strings.HasPrefix(name, ".size.") {
				return false
			}
		}

		return true
	}, time.Second, time.Millisecond, "async cache operations should complete in %s", dir)
}

// =============================================================================
// Uncompressed GetFrame Tests
// =============================================================================

func TestCache_GetFrame_Uncompressed_CacheHit(t *testing.T) {
	t.Parallel()

	const chunkSize = 4096
	data := makeTestData(chunkSize, 0)

	c := newTestCache(t, nil, chunkSize) // nil inner - cache hit shouldn't need it

	// Pre-populate cache
	chunkPath := c.makeChunkFilename("obj", 0)
	writeToCache(t, chunkPath, data)

	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, chunkSize)

	rng, err := c.GetFrame(t.Context(), "obj", 0, frameTable, false, buf)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, chunkSize, rng.Length)
	assert.Equal(t, data, buf)
}

func TestCache_GetFrame_Uncompressed_CacheMiss(t *testing.T) {
	t.Parallel()

	const chunkSize = 4096
	data := makeTestData(chunkSize, 42)

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), mock.Anything, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, data)
		}).
		Return(Range{Start: 0, Length: chunkSize}, nil).Once()

	c := newTestCache(t, inner, chunkSize)

	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, chunkSize)

	rng, err := c.GetFrame(t.Context(), "obj", 0, frameTable, false, buf)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, chunkSize, rng.Length)
	assert.Equal(t, data, buf)

	// Verify write-back to cache
	chunkPath := c.makeChunkFilename("obj", 0)
	waitForCacheFile(t, chunkPath)

	cached, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	assert.Equal(t, data, cached)
}

func TestCache_GetFrame_Uncompressed_NonZeroOffset(t *testing.T) {
	t.Parallel()

	const chunkSize = 4096
	data := makeTestData(chunkSize, 99)

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(chunkSize*5), mock.Anything, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, data)
		}).
		Return(Range{Start: int64(chunkSize * 5), Length: chunkSize}, nil).Once()

	c := newTestCache(t, inner, chunkSize)

	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, chunkSize)

	rng, err := c.GetFrame(t.Context(), "obj", int64(chunkSize*5), frameTable, false, buf)

	require.NoError(t, err)
	assert.Equal(t, int64(chunkSize*5), rng.Start)
	assert.Equal(t, chunkSize, rng.Length)
	assert.Equal(t, data, buf)

	// Wait for async cache write to complete
	chunkPath := c.makeChunkFilename("obj", int64(chunkSize*5))
	waitForCacheFile(t, chunkPath)
}

func TestCache_GetFrame_Uncompressed_InnerError(t *testing.T) {
	t.Parallel()

	const chunkSize = 4096
	expectedErr := errors.New("network error")

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), mock.Anything, false, mock.Anything).
		Return(Range{}, expectedErr)

	c := newTestCache(t, inner, chunkSize)

	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, chunkSize)

	_, err := c.GetFrame(t.Context(), "obj", 0, frameTable, false, buf)

	require.Error(t, err)
	require.Contains(t, err.Error(), "uncached read")
}

// =============================================================================
// Compressed GetFrame Tests
// =============================================================================

func TestCache_GetFrame_Compressed_CacheHit_Decompress(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	c := newTestCache(t, nil, uncompressedSize) // nil inner - cache hit

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	// Pre-populate cache
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	writeToCache(t, framePath, compressed)

	buf := make([]byte, uncompressedSize)
	// Use internal method with cacheCompressed=true to test cache behavior
	// (bypasses EnableNFSCompressedCache flag)
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start) // uncompressed offset
	assert.Equal(t, uncompressedSize, rng.Length)
	assert.Equal(t, origData, buf)
}

func TestCache_GetFrame_Compressed_CacheHit_Raw(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	c := newTestCache(t, nil, uncompressedSize)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	writeToCache(t, framePath, compressed)

	buf := make([]byte, len(compressed))
	// Use internal method with cacheCompressed=true to test cache behavior
	// (bypasses EnableNFSCompressedCache flag)
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, false, buf, true)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start) // compressed offset
	assert.Equal(t, len(compressed), rng.Length)
	assert.Equal(t, compressed, buf[:rng.Length])
}

func TestCache_GetFrame_Compressed_CacheMiss_Decompress(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once()

	c := newTestCache(t, inner, uncompressedSize)

	buf := make([]byte, uncompressedSize)
	// Use internal method with cacheCompressed=true to test cache behavior
	// (bypasses EnableNFSCompressedCache flag)
	rng, wg, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, uncompressedSize, rng.Length)
	assert.Equal(t, origData, buf)

	// Wait for async cache write
	wg.Wait()

	// Verify compressed data was cached
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	waitForCacheFile(t, framePath)

	cached, err := os.ReadFile(framePath)
	require.NoError(t, err)
	assert.Equal(t, compressed, cached)
}

func TestCache_GetFrame_Compressed_CacheMiss_Raw(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once()

	c := newTestCache(t, inner, uncompressedSize)

	buf := make([]byte, len(compressed))
	// Use internal method with cacheCompressed=true to test cache behavior
	// (bypasses EnableNFSCompressedCache flag)
	rng, wg, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, false, buf, true)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, len(compressed), rng.Length)
	assert.Equal(t, compressed, buf[:rng.Length])

	// Wait for async cache write to complete before test cleanup
	wg.Wait()
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	waitForCacheFile(t, framePath)
}

func TestCache_GetFrame_Compressed_RoundTrip(t *testing.T) {
	t.Parallel()

	const dataSize = 16 * 1024
	origData := makeTestData(dataSize, 0)
	compressed := compressTestData(t, origData)

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          []FrameSize{{U: int32(dataSize), C: int32(len(compressed))}},
	}

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once() // Only called once

	c := newTestCache(t, inner, dataSize)

	// First read - cache miss
	// Use internal method with cacheCompressed=true to test cache behavior
	buf1 := make([]byte, dataSize)
	rng1, wg, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf1, true)
	require.NoError(t, err)
	assert.Equal(t, dataSize, rng1.Length)
	assert.Equal(t, origData, buf1)

	// Wait for cache write
	wg.Wait()
	framePath := c.makeFrameFilename("obj", FrameOffset{U: 0, C: 0},
		FrameSize{U: int32(dataSize), C: int32(len(compressed))})
	waitForCacheFile(t, framePath)

	// Second read - cache hit (mock expectation .Once() ensures no second call)
	buf2 := make([]byte, dataSize)
	rng2, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf2, true)
	require.NoError(t, err)
	assert.Equal(t, dataSize, rng2.Length)
	assert.Equal(t, origData, buf2)
}

func TestCache_GetFrame_Compressed_MultipleFrames(t *testing.T) {
	t.Parallel()

	const frameSz = 4096
	const numFrames = 4

	// Create frame data
	frames := make([][]byte, numFrames)
	compressedFrames := make([][]byte, numFrames)
	frameSizes := make([]FrameSize, numFrames)

	for i := range numFrames {
		frames[i] = makeTestData(frameSz, byte(i*50))
		compressedFrames[i] = compressTestData(t, frames[i])
		frameSizes[i] = FrameSize{
			U: int32(frameSz),
			C: int32(len(compressedFrames[i])),
		}
	}

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          frameSizes,
	}

	// Calculate offsets
	offsets := make([]FrameOffset, numFrames)
	for i := 1; i < numFrames; i++ {
		offsets[i].U = offsets[i-1].U + int64(frameSizes[i-1].U)
		offsets[i].C = offsets[i-1].C + int64(frameSizes[i-1].C)
	}

	c := newTestCache(t, nil, frameSz)

	// Pre-populate cache for frames 0 and 2
	writeToCache(t, c.makeFrameFilename("obj", offsets[0], frameSizes[0]), compressedFrames[0])
	writeToCache(t, c.makeFrameFilename("obj", offsets[2], frameSizes[2]), compressedFrames[2])

	// Mock for frames 1 and 3 (cache misses)
	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", offsets[1].U, frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressedFrames[1])
		}).
		Return(Range{Start: offsets[1].C, Length: len(compressedFrames[1])}, nil).Once()
	inner.EXPECT().GetFrame(mock.Anything, "obj", offsets[3].U, frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressedFrames[3])
		}).
		Return(Range{Start: offsets[3].C, Length: len(compressedFrames[3])}, nil).Once()

	c.inner = inner

	// Read all frames using internal method with cacheCompressed=true
	var wgs []*sync.WaitGroup
	for i := range numFrames {
		buf := make([]byte, frameSz)
		rng, wg, err := c.getCompressedFrameInternal(t.Context(), "obj", offsets[i].U, frameTable, true, buf, true)
		require.NoError(t, err, "frame %d", i)
		assert.Equal(t, frameSz, rng.Length, "frame %d", i)
		assert.Equal(t, frames[i], buf, "frame %d data mismatch", i)
		wgs = append(wgs, wg)
	}

	// Wait for all async cache writes
	for _, wg := range wgs {
		wg.Wait()
	}

	// Wait for cache writes
	waitForCacheFile(t, c.makeFrameFilename("obj", offsets[1], frameSizes[1]))
	waitForCacheFile(t, c.makeFrameFilename("obj", offsets[3], frameSizes[3]))
}

func TestCache_GetFrame_Compressed_InnerError(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          []FrameSize{{U: int32(uncompressedSize), C: int32(len(compressed))}},
	}

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Return(Range{}, errors.New("backend unavailable"))

	c := newTestCache(t, inner, uncompressedSize)

	buf := make([]byte, uncompressedSize)
	// Use internal method with cacheCompressed=true to test cache behavior
	_, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

	require.Error(t, err)
	require.Contains(t, err.Error(), "uncached read")
}

func TestCache_GetFrame_Compressed_NonZeroStartAt(t *testing.T) {
	t.Parallel()

	// Test frame table that starts at non-zero offset (e.g., partial file)
	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	startAt := FrameOffset{U: 8192, C: 2048}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         startAt,
		Frames:          []FrameSize{frameSize},
	}

	c := newTestCache(t, nil, uncompressedSize)

	// Pre-populate cache - frame offset includes startAt
	frameOffset := FrameOffset{U: startAt.U, C: startAt.C}
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	writeToCache(t, framePath, compressed)

	buf := make([]byte, uncompressedSize)
	// Use internal method with cacheCompressed=true to test cache behavior
	// (bypasses EnableNFSCompressedCache flag)
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", startAt.U, frameTable, true, buf, true)

	require.NoError(t, err)
	assert.Equal(t, startAt.U, rng.Start)
	assert.Equal(t, uncompressedSize, rng.Length)
	assert.Equal(t, origData, buf)
}

// =============================================================================
// StoreFile Tests
// =============================================================================

func TestCache_StoreFile_Uncompressed_NoWriteThrough(t *testing.T) {
	t.Parallel()

	data := []byte("test data for storage")
	inputFile := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inputFile, data, 0o644))

	inner := NewMockStorageProvider(t)
	inner.EXPECT().StoreFile(mock.Anything, inputFile, "obj", (*FramedUploadOptions)(nil)).
		Return((*FrameTable)(nil), nil)

	c, flags := newTestCacheWithFlags(t, inner, 1024)
	flags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(false) // disable write-through

	ft, err := c.StoreFile(t.Context(), inputFile, "obj", nil)

	require.NoError(t, err)
	assert.Nil(t, ft)
}

func TestCache_StoreFile_Uncompressed_WriteThrough(t *testing.T) {
	t.Parallel()

	data := makeTestData(8192, 0)
	inputFile := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inputFile, data, 0o644))

	inner := NewMockStorageProvider(t)
	inner.EXPECT().StoreFile(mock.Anything, inputFile, "obj", (*FramedUploadOptions)(nil)).
		Return((*FrameTable)(nil), nil)

	c, flags := newTestCacheWithFlags(t, inner, 4096)
	flags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(true) // enable write-through
	flags.EXPECT().IntFlag(mock.Anything, mock.Anything).Return(2)     // concurrency

	ft, err := c.StoreFile(t.Context(), inputFile, "obj", nil)

	require.NoError(t, err)
	assert.Nil(t, ft)

	// Verify chunks were written to cache
	waitForCacheFile(t, c.makeChunkFilename("obj", 0))
	waitForCacheFile(t, c.makeChunkFilename("obj", 4096))
	waitForCacheFile(t, c.sizeFilename("obj"))

	// Verify content
	chunk0, err := os.ReadFile(c.makeChunkFilename("obj", 0))
	require.NoError(t, err)
	assert.Equal(t, data[:4096], chunk0)

	chunk1, err := os.ReadFile(c.makeChunkFilename("obj", 4096))
	require.NoError(t, err)
	assert.Equal(t, data[4096:], chunk1)
}

func TestCache_StoreFile_Compressed_WritesFramesToCache(t *testing.T) {
	t.Parallel()
	if !EnableGCSCompression {
		t.Skip("skipping compression test when EnableGCSCompression is false")
	}

	const dataSize = 5 * 1024 * 1024 // 5MB to ensure we get at least one full frame
	origData := makeRepetitiveData(dataSize)

	inputFile := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inputFile, origData, 0o644))

	storageRoot := t.TempDir()
	innerStorage := &Storage{Backend: NewFS(storageRoot)}

	opts := &FramedUploadOptions{
		CompressionType: CompressionZstd,
		ChunkSize:       MemoryChunkSize, // Must be multiple of MemoryChunkSize
		TargetFrameSize: MemoryChunkSize,
		Level:           int(zstd.SpeedBestCompression),
	}

	c, _ := newTestCacheWithFlags(t, innerStorage, MemoryChunkSize)

	// Use storeCompressed directly to get the WaitGroup for async writes
	ft, wg, err := c.storeCompressed(t.Context(), inputFile, "obj", opts)

	require.NoError(t, err)
	require.NotNil(t, ft)
	assert.Equal(t, CompressionZstd, ft.CompressionType)
	require.NotEmpty(t, ft.Frames)

	// Verify total uncompressed size
	var totalU int64
	for _, f := range ft.Frames {
		totalU += int64(f.U)
	}
	assert.Equal(t, int64(dataSize), totalU)

	// Wait for async cache writes to complete
	wg.Wait()

	// Verify each cached frame decompresses correctly
	var offset FrameOffset
	for i, frame := range ft.Frames {
		framePath := c.makeFrameFilename("obj", offset, frame)
		cached, err := os.ReadFile(framePath)
		require.NoError(t, err, "frame %d", i)
		require.Len(t, cached, int(frame.C), "frame %d size", i)

		buf := make([]byte, frame.U)
		n, err := decompressBytes(t.Context(), CompressionZstd, cached, buf)
		require.NoError(t, err, "frame %d decompress", i)
		require.Equal(t, int(frame.U), n, "frame %d decompress size", i)

		expectedSlice := origData[offset.U : offset.U+int64(frame.U)]
		assert.Equal(t, expectedSlice, buf, "frame %d content", i)

		offset.Add(frame)
	}
}

func TestCache_StoreFile_InnerError(t *testing.T) {
	t.Parallel()

	inputFile := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inputFile, []byte("data"), 0o644))

	inner := NewMockStorageProvider(t)
	inner.EXPECT().StoreFile(mock.Anything, inputFile, "obj", (*FramedUploadOptions)(nil)).
		Return((*FrameTable)(nil), errors.New("upload failed"))

	c, flags := newTestCacheWithFlags(t, inner, 1024)
	flags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(false)

	_, err := c.StoreFile(t.Context(), inputFile, "obj", nil)

	require.Error(t, err)
}

// =============================================================================
// Data Integrity Tests
// =============================================================================

func TestCache_GetFrame_DataIntegrity(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		data []byte
	}{
		{"zeros", make([]byte, 4096)},
		{"sequential", makeTestData(4096, 0)},
		{"repetitive", makeRepetitiveData(4096)},
		{"high entropy", func() []byte {
			d := make([]byte, 4096)
			for i := range d {
				d[i] = byte((i*7 + i*i*3) % 256)
			}

			return d
		}()},
		{"single byte", []byte{0x42}},
		{"two bytes", []byte{0x00, 0xFF}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			compressed := compressTestData(t, tc.data)
			frameTable := singleFrameTable(CompressionZstd, len(tc.data), len(compressed))

			c := newTestCache(t, nil, int64(len(tc.data)))

			frameOffset := FrameOffset{U: 0, C: 0}
			frameSz := FrameSize{U: int32(len(tc.data)), C: int32(len(compressed))}
			framePath := c.makeFrameFilename("obj", frameOffset, frameSz)
			writeToCache(t, framePath, compressed)

			buf := make([]byte, len(tc.data))
			// Use internal method with cacheCompressed=true to test cache behavior
			rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

			require.NoError(t, err)
			assert.Equal(t, len(tc.data), rng.Length)
			assert.Equal(t, tc.data, buf[:rng.Length])
		})
	}
}

// =============================================================================
// Concurrency Tests
// =============================================================================

func TestCache_GetFrame_ConcurrentReads(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSz := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSz},
	}

	c := newTestCache(t, nil, uncompressedSize)

	framePath := c.makeFrameFilename("obj", frameOffset, frameSz)
	writeToCache(t, framePath, compressed)

	const numReaders = 10
	var wg sync.WaitGroup
	errs := make(chan error, numReaders)

	for range numReaders {
		wg.Go(func() {
			buf := make([]byte, uncompressedSize)
			// Use internal method with cacheCompressed=true to test cache behavior
			rng, _, err := c.getCompressedFrameInternal(context.Background(), "obj", 0, frameTable, true, buf, true)
			if err != nil {
				errs <- err

				return
			}
			if rng.Length != uncompressedSize {
				errs <- errors.New("wrong length")

				return
			}
			if !bytes.Equal(buf, origData) {
				errs <- errors.New("data mismatch")

				return
			}
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestCache_GetFrame_ConcurrentCacheMisses(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSz := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSz},
	}

	inner := NewMockStorageProvider(t)
	// Allow multiple calls since concurrent readers may all miss cache initially
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Maybe()

	c := newTestCache(t, inner, uncompressedSize)

	const numReaders = 5
	var wg sync.WaitGroup
	errs := make(chan error, numReaders)

	for range numReaders {
		wg.Go(func() {
			buf := make([]byte, uncompressedSize)
			// Use internal method with cacheCompressed=true to test cache behavior
			rng, _, err := c.getCompressedFrameInternal(context.Background(), "obj", 0, frameTable, true, buf, true)
			if err != nil {
				errs <- err

				return
			}
			if rng.Length != uncompressedSize {
				errs <- errors.New("wrong length")

				return
			}
			if !bytes.Equal(buf, origData) {
				errs <- errors.New("data mismatch")

				return
			}
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	// Verify cache was populated
	framePath := c.makeFrameFilename("obj", frameOffset, frameSz)
	waitForCacheFile(t, framePath)
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestCache_GetFrame_EmptyBuffer(t *testing.T) {
	t.Parallel()

	c := newTestCache(t, nil, 4096)
	frameTable := singleFrameTable(CompressionZstd, 4096, 1024)

	_, err := c.GetFrame(t.Context(), "obj", 0, frameTable, true, []byte{})

	require.ErrorIs(t, err, ErrBufferTooSmall)
}

func TestCache_GetFrame_UnalignedOffset(t *testing.T) {
	t.Parallel()

	c := newTestCache(t, nil, 4096)
	frameTable := singleFrameTable(CompressionZstd, 4096, 1024)

	_, err := c.GetFrame(t.Context(), "obj", 100, frameTable, true, make([]byte, 4096))

	require.ErrorIs(t, err, ErrOffsetUnaligned)
}

// =============================================================================
// readAtFromCache / decompressFromCache Tests
// =============================================================================

func TestCache_readAtFromCache(t *testing.T) {
	t.Parallel()

	data := makeTestData(1024, 0)
	c := newTestCache(t, nil, 4096)

	chunkPath := filepath.Join(c.rootPath, "test-chunk.bin")
	writeToCache(t, chunkPath, data)

	buf := make([]byte, len(data))
	n, err := c.readAtFromCache(t.Context(), chunkPath, buf)

	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	assert.Equal(t, data, buf)
}

func TestCache_readAtFromCache_FileNotExists(t *testing.T) {
	t.Parallel()

	c := newTestCache(t, nil, 4096)

	buf := make([]byte, 1024)
	_, err := c.readAtFromCache(t.Context(), "/nonexistent/path", buf)

	require.Error(t, err)
	require.True(t, os.IsNotExist(errors.Unwrap(err)))
}

func TestCache_decompressFromCache(t *testing.T) {
	t.Parallel()

	origData := makeRepetitiveData(4096)
	compressed := compressTestData(t, origData)

	c := newTestCache(t, nil, 4096)

	chunkPath := filepath.Join(c.rootPath, "test-frame.frm")
	writeToCache(t, chunkPath, compressed)

	buf := make([]byte, len(origData))
	n, err := c.decompressFromCache(t.Context(), chunkPath, CompressionZstd, buf)

	require.NoError(t, err)
	assert.Equal(t, len(origData), n)
	assert.Equal(t, origData, buf)
}

func TestCache_decompressFromCache_CorruptedData(t *testing.T) {
	t.Parallel()

	c := newTestCache(t, nil, 4096)

	chunkPath := filepath.Join(c.rootPath, "corrupted.frm")
	writeToCache(t, chunkPath, []byte("not valid zstd data"))

	buf := make([]byte, 4096)
	_, err := c.decompressFromCache(t.Context(), chunkPath, CompressionZstd, buf)

	require.Error(t, err)
}

// =============================================================================
// writeChunkToCache / writeFrameToCache Tests
// =============================================================================

func TestCache_writeChunkToCache(t *testing.T) {
	t.Parallel()

	data := makeTestData(1024, 42)
	c := newTestCache(t, nil, 4096)

	chunkPath := c.makeChunkFilename("obj", 0)
	err := c.writeChunkToCache(t.Context(), "obj", 0, chunkPath, data)

	require.NoError(t, err)

	cached, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	assert.Equal(t, data, cached)
}

func TestCache_writeFrameToCache(t *testing.T) {
	t.Parallel()

	data := makeTestData(1024, 42)
	c := newTestCache(t, nil, 4096)

	offset := FrameOffset{U: 0, C: 0}
	chunkPath := c.makeFrameFilename("obj", offset, FrameSize{U: 4096, C: 1024})
	err := c.writeFrameToCache(t.Context(), offset, chunkPath, data)

	require.NoError(t, err)

	cached, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	assert.Equal(t, data, cached)
}

// =============================================================================
// Cache invalidation via corrupted file
// =============================================================================

func TestCache_GetFrame_CorruptedCacheFile_FallsBackToInner(t *testing.T) {
	t.Parallel()

	const uncompressedSize = 4096
	origData := makeRepetitiveData(uncompressedSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(uncompressedSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once()

	c := newTestCache(t, inner, uncompressedSize)

	// Pre-populate with corrupted data
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	writeToCache(t, framePath, []byte("corrupted data that is not valid zstd"))

	buf := make([]byte, uncompressedSize)
	// Use internal method to get WaitGroup for async cache write
	rng, wg, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

	// Should succeed by falling back to inner
	require.NoError(t, err)
	assert.Equal(t, uncompressedSize, rng.Length)
	assert.Equal(t, origData, buf)

	// Wait for async cache write to complete before test cleanup
	wg.Wait()
}

// =============================================================================
// Integration-style Tests
// =============================================================================

func TestCache_FullWorkflow_StoreAndRetrieve(t *testing.T) {
	t.Parallel()
	if !EnableGCSCompression {
		t.Skip("skipping compression test when EnableGCSCompression is false")
	}

	const dataSize = 5 * 1024 * 1024 // 5MB to ensure we get at least one full frame
	origData := makeTestData(dataSize, 123)

	inputFile := filepath.Join(t.TempDir(), "input.bin")
	require.NoError(t, os.WriteFile(inputFile, origData, 0o644))

	storageRoot := t.TempDir()
	innerStorage := &Storage{Backend: NewFS(storageRoot)}

	opts := &FramedUploadOptions{
		CompressionType: CompressionZstd,
		ChunkSize:       MemoryChunkSize, // Must be multiple of MemoryChunkSize
		TargetFrameSize: MemoryChunkSize,
		Level:           int(zstd.SpeedDefault),
	}

	c, flags := newTestCacheWithFlags(t, innerStorage, MemoryChunkSize)
	flags.EXPECT().BoolFlag(mock.Anything, mock.Anything).Return(false).Maybe()

	// Store
	ft, err := c.StoreFile(t.Context(), inputFile, "test-object", opts)
	require.NoError(t, err)
	require.NotNil(t, ft)

	// Wait for cache writes
	require.Eventually(t, func() bool {
		var offset FrameOffset
		for _, frame := range ft.Frames {
			framePath := c.makeFrameFilename("test-object", offset, frame)
			if _, err := os.Stat(framePath); err != nil {
				return false
			}
			offset.Add(frame)
		}

		return true
	}, 2*time.Second, 10*time.Millisecond)

	// Read back all frames and reconstruct data
	reconstructed := make([]byte, 0, dataSize)
	var offset FrameOffset
	for _, frame := range ft.Frames {
		buf := make([]byte, frame.U)
		rng, err := c.GetFrame(t.Context(), "test-object", offset.U, ft, true, buf)
		require.NoError(t, err)
		reconstructed = append(reconstructed, buf[:rng.Length]...)
		offset.Add(frame)
	}

	assert.Equal(t, origData, reconstructed)
}

// =============================================================================
// Partial Read Tests (buffer smaller than frame)
// =============================================================================

func TestCache_GetFrame_Uncompressed_PartialRead(t *testing.T) {
	t.Parallel()

	const chunkSize = 4096
	data := makeTestData(chunkSize, 0)

	c := newTestCache(t, nil, chunkSize)

	chunkPath := c.makeChunkFilename("obj", 0)
	writeToCache(t, chunkPath, data)

	// Request smaller buffer than chunk size
	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, 1024) // smaller than chunk

	rng, err := c.GetFrame(t.Context(), "obj", 0, frameTable, false, buf)

	require.NoError(t, err)
	assert.Equal(t, 1024, rng.Length)
	assert.Equal(t, data[:1024], buf)
}

// =============================================================================
// Parameterized Cache Mode Tests (cacheCompressed flag)
// =============================================================================

func TestCache_getCompressedFrameInternal_Passthrough_Decompress(t *testing.T) {
	t.Parallel()

	// When cacheCompressed=false, pass through to inner without caching
	const chunkSize = 4096
	origData := makeRepetitiveData(chunkSize)
	compressed := compressTestData(t, origData)

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          []FrameSize{{U: int32(chunkSize), C: int32(len(compressed))}},
	}

	// Inner is asked with the same decompress flag we pass
	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, true, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, origData) // Inner returns decompressed data
		}).
		Return(Range{Start: 0, Length: chunkSize}, nil).Once()

	c := newTestCache(t, inner, chunkSize)

	buf := make([]byte, chunkSize)
	// cacheCompressed=false -> passthrough to inner
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, false)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, chunkSize, rng.Length)
	assert.Equal(t, origData, buf)

	// No cache file should be written
	time.Sleep(50 * time.Millisecond) // Give time for any async write
	chunkPath := c.makeChunkFilename("obj", 0)
	_, err = os.Stat(chunkPath)
	assert.True(t, os.IsNotExist(err), "no cache file should be written in passthrough mode")
}

func TestCache_getCompressedFrameInternal_Passthrough_Raw(t *testing.T) {
	t.Parallel()

	// When cacheCompressed=false and caller wants raw compressed data, just pass through
	const chunkSize = 4096
	origData := makeRepetitiveData(chunkSize)
	compressed := compressTestData(t, origData)

	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         FrameOffset{U: 0, C: 0},
		Frames:          []FrameSize{{U: int32(chunkSize), C: int32(len(compressed))}},
	}

	// Raw read passes through to inner without caching
	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once()

	c := newTestCache(t, inner, chunkSize)

	buf := make([]byte, len(compressed))
	// cacheCompressed=false, decompress=false -> passthrough
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, false, buf, false)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, len(compressed), rng.Length)
	assert.Equal(t, compressed, buf[:rng.Length])
}

func TestCache_getCompressedFrameInternal_CompressedFrameMode(t *testing.T) {
	t.Parallel()

	// When cacheCompressed=true (default), cache compressed frames
	const chunkSize = 4096
	origData := makeRepetitiveData(chunkSize)
	compressed := compressTestData(t, origData)

	frameOffset := FrameOffset{U: 0, C: 0}
	frameSize := FrameSize{U: int32(chunkSize), C: int32(len(compressed))}
	frameTable := &FrameTable{
		CompressionType: CompressionZstd,
		StartAt:         frameOffset,
		Frames:          []FrameSize{frameSize},
	}

	// Inner returns compressed data
	inner := NewMockStorageProvider(t)
	inner.EXPECT().GetFrame(mock.Anything, "obj", int64(0), frameTable, false, mock.Anything).
		Run(func(_ context.Context, _ string, _ int64, _ *FrameTable, _ bool, buf []byte) {
			copy(buf, compressed)
		}).
		Return(Range{Start: 0, Length: len(compressed)}, nil).Once()

	c := newTestCache(t, inner, chunkSize)

	buf := make([]byte, chunkSize)
	// cacheCompressed=true -> cache compressed frames
	rng, _, err := c.getCompressedFrameInternal(t.Context(), "obj", 0, frameTable, true, buf, true)

	require.NoError(t, err)
	assert.Equal(t, int64(0), rng.Start)
	assert.Equal(t, chunkSize, rng.Length)
	assert.Equal(t, origData, buf)

	// Verify compressed data was cached (.frm)
	framePath := c.makeFrameFilename("obj", frameOffset, frameSize)
	waitForCacheFile(t, framePath)

	cached, err := os.ReadFile(framePath)
	require.NoError(t, err)
	assert.Equal(t, compressed, cached)
}

// =============================================================================
// EOF handling
// =============================================================================

func TestCache_GetFrame_Uncompressed_EOF(t *testing.T) {
	t.Parallel()

	// Test that EOF from readAtFromCache is handled correctly
	const chunkSize = 4096
	// Write less data than buffer expects
	data := makeTestData(2048, 0)

	c := newTestCache(t, nil, chunkSize)

	chunkPath := c.makeChunkFilename("obj", 0)
	writeToCache(t, chunkPath, data)

	frameTable := singleFrameTable(CompressionNone, chunkSize, chunkSize)
	buf := make([]byte, chunkSize)

	rng, err := c.GetFrame(t.Context(), "obj", 0, frameTable, false, buf)

	// Should get EOF but still return what was read
	require.ErrorIs(t, err, io.EOF)
	assert.Equal(t, 2048, rng.Length)
	assert.Equal(t, data, buf[:2048])
}

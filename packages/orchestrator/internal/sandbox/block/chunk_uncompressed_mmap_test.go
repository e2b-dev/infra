package block

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

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
			end := offset + int64(len(buf))
			if end > int64(len(data)) {
				end = int64(len(data))
			}
			if offset >= int64(len(data)) {
				return storage.Range{Start: offset, Length: 0}, nil
			}

			n := copy(buf, data[offset:end])

			return storage.Range{Start: offset, Length: n}, nil
		}).Maybe()

	return mockStorage
}

func TestUncompressedMMapChunker_BasicSlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data - 4MB uncompressed
	dataSize := int64(4 * 1024 * 1024)
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

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
	defer chunker.Close()

	// Test basic slice at offset 0
	slice, err := chunker.Slice(ctx, 0, 1024, nil)
	require.NoError(t, err)
	assert.Len(t, slice, 1024)
	assert.Equal(t, testData[:1024], slice)

	// Test larger slice from same block (cache hit)
	slice, err = chunker.Slice(ctx, 0, 2048, nil)
	require.NoError(t, err)
	assert.Len(t, slice, 2048)
	assert.Equal(t, testData[:2048], slice)
}

func TestUncompressedMMapChunker_CachePersists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dataSize := int64(4 * 1024 * 1024)
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

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

func TestUncompressedMMapChunker_FileSize(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dataSize := int64(4 * 1024 * 1024)
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

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
	defer chunker.Close()

	// FileSize returns on-disk sparse file size (0 before fetching data)
	size, err := chunker.FileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(0), size, "sparse file should have 0 on-disk size before data is fetched")

	// Fetch some data to populate the cache
	_, err = chunker.Slice(ctx, 0, 4096, nil)
	require.NoError(t, err)

	// After fetching, FileSize should be non-zero (but may vary by filesystem)
	size, err = chunker.FileSize()
	require.NoError(t, err)
	assert.Positive(t, size, "on-disk size should be non-zero after fetching data")
}

func TestUncompressedMMapChunker_EmptySlice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	dataSize := int64(4 * 1024 * 1024)
	testData := make([]byte, dataSize)

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
	defer chunker.Close()

	// Request zero-length slice
	slice, err := chunker.Slice(ctx, 0, 0, nil)
	require.NoError(t, err)
	assert.Empty(t, slice)
}

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
			end := min(offset+int64(len(buf)), int64(len(data)))
			if offset >= int64(len(data)) {
				return storage.Range{Start: offset, Length: 0}, nil
			}

			n := copy(buf, data[offset:end])

			return storage.Range{Start: offset, Length: n}, nil
		}).Maybe()

	return mockStorage
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

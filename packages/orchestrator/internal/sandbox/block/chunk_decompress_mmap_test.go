package block

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// setupMockStorageDecompress creates a MockStorageProvider for DecompressMMapChunker tests.
// It handles both compressed frame retrieval (decompress=false) for the chunker's internal use.
func setupMockStorageDecompress(t *testing.T, frames map[int64][]byte) *storage.MockStorageProvider {
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

func TestDecompressMMapChunker_ReadAt(t *testing.T) {
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

	mockStorage := setupMockStorageDecompress(t, map[int64][]byte{0: compressedData})

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
	buf := make([]byte, 2048)
	n, err := func() (int, error) {
		s, e := chunker.Slice(ctx, 0, int64(len(buf)), frameTable)
		if e != nil {
			return 0, e
		}

		return copy(buf, s), nil
	}()
	require.NoError(t, err)
	assert.Equal(t, 2048, n)
	assert.Equal(t, uncompressedData[0:2048], buf)
}

func TestDecompressMMapChunker_CachePersists(t *testing.T) {
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

	mockStorage := setupMockStorageDecompress(t, map[int64][]byte{0: compressedData})

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

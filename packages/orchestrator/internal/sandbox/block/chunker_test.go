package block

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// TestChunker_Interface verifies all chunker implementations satisfy the Chunker interface
// and work correctly through that interface.
func TestChunker_Interface_CompressLRU(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Create test data
	dataSize := 4 * 1024 * 1024 // 4MB
	uncompressedData := make([]byte, dataSize)
	for i := range uncompressedData {
		uncompressedData[i] = byte(i % 256)
	}
	compressedData := compressData(t, uncompressedData)

	frameTable := &storage.FrameTable{
		CompressionType: storage.CompressionZstd,
		StartAt:         storage.FrameOffset{U: 0, C: 0},
		Frames: []storage.FrameSize{
			{U: int32(dataSize), C: int32(len(compressedData))},
		},
	}

	mockStorage := setupMockStorage(t, map[int64][]byte{0: compressedData})

	// Create chunker and use through interface
	var chunker Chunker
	var err error
	chunker, err = NewCompressLRUChunker(
		int64(dataSize), // virtSize (uncompressed)
		mockStorage,
		"test/path",
		10,
		testMetrics(t),
	)
	require.NoError(t, err)
	defer chunker.Close()

	// Test Slice with copy (replaces ReadAt)
	buf := make([]byte, 1024)
	slice, err := chunker.Slice(ctx, 0, 1024, frameTable)
	require.NoError(t, err)
	n := copy(buf, slice)
	assert.Equal(t, 1024, n)
	assert.Equal(t, uncompressedData[:1024], buf)

	// Test Slice at different offset with copy
	buf = make([]byte, 500)
	slice, err = chunker.Slice(ctx, 1000, 500, frameTable)
	require.NoError(t, err)
	n = copy(buf, slice)
	assert.Equal(t, 500, n)
	assert.Equal(t, uncompressedData[1000:1500], buf)

	// Test Slice
	slice, err = chunker.Slice(ctx, 0, 1024, frameTable)
	require.NoError(t, err)
	assert.Len(t, slice, 1024)
	assert.Equal(t, uncompressedData[:1024], slice)

	// Test Slice at different offset
	slice, err = chunker.Slice(ctx, 2048, 512, frameTable)
	require.NoError(t, err)
	assert.Len(t, slice, 512)
	assert.Equal(t, uncompressedData[2048:2560], slice)

	// Test FileSize - CompressLRUChunker returns 0 (no local disk files, pure in-memory LRU)
	size, err := chunker.FileSize()
	require.NoError(t, err)
	assert.Equal(t, int64(0), size, "CompressLRUChunker has no disk files, FileSize should be 0")
}

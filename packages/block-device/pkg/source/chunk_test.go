package source

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunker(t *testing.T) {
	size := ChunkSize * 10
	baseData := bytes.Repeat([]byte("H"), int(size))
	base := block.NewMockDevice(baseData)
	cache := block.NewMockDevice(make([]byte, len(baseData)))

	// Create a new Chunker
	ctx := context.Background()
	chunker := NewChunker(ctx, base, cache)
	defer chunker.Close()

	// Read data from the Chunker
	buf := make([]byte, len(baseData))
	n, err := chunker.ReadAt(buf, 0)
	require.NoError(t, err)
	assert.Equal(t, len(baseData), n)
	assert.Equal(t, baseData, buf)

	// Read a chunk of data from the Chunker
	chunkBuf := make([]byte, ChunkSize)
	n, err = chunker.ReadAt(chunkBuf, ChunkSize)
	require.NoError(t, err)
	assert.Equal(t, ChunkSize, n)
	assert.Equal(t, baseData[ChunkSize:ChunkSize*2], chunkBuf)

	// Prefetch a chunk
	_, err = chunker.ReadAt(nil, ChunkSize*2)
	require.NoError(t, err)

	// Read the prefetched chunk
	prefetchedBuf := make([]byte, ChunkSize)
	n, err = chunker.ReadAt(prefetchedBuf, ChunkSize*2)
	require.NoError(t, err)
	assert.Equal(t, ChunkSize, n)
	assert.Equal(t, baseData[ChunkSize*2:ChunkSize*3], prefetchedBuf)

	// Read beyond the end of the base reader
	_, err = chunker.ReadAt(buf, int64(len(baseData)))
	assert.Equal(t, io.EOF, err)
}

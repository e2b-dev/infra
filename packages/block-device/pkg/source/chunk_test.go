package source

import (
	"bytes"
	"context"
	"testing"

	"github.com/e2b-dev/infra/packages/block-device/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChunkerReadBlock(t *testing.T) {
	size := ChunkSize * 2
	baseData := bytes.Repeat([]byte("H"), int(size))
	base := block.NewMockDevice(baseData, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), false)

	// Create a new Chunker
	ctx, cancel := context.WithCancel(context.Background())
	chunker := NewChunker(ctx, base, cache)
	defer cancel()

	// Read data from the Chunker
	buf := make([]byte, block.Size)
	_, err := chunker.ReadAt(buf, 0)
	require.NoError(t, err)

	// The check for total equality takes a really long time
	assert.EqualValues(t, baseData[0], buf[0])
	assert.EqualValues(t, baseData[block.Size-1], buf[block.Size-1])
}

func TestChunkerReadSmaller(t *testing.T) {
	size := ChunkSize * 3
	baseData := bytes.Repeat([]byte("H"), int(size))
	base := block.NewMockDevice(baseData, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), false)

	// Create a new Chunker
	ctx, cancel := context.WithCancel(context.Background())
	chunker := NewChunker(ctx, base, cache)
	defer cancel()

	smallBlockSize := block.Size / 4
	// Read a chunk of data from the Chunker
	chunkBuf := make([]byte, smallBlockSize)
	_, err := chunker.ReadAt(chunkBuf, smallBlockSize)
	require.NoError(t, err)

	// The check for total equality takes a really long time
	assert.EqualValues(t, baseData[block.Size], chunkBuf[0])
	assert.EqualValues(t, baseData[block.Size+smallBlockSize-1], chunkBuf[smallBlockSize-1])
}

func TestChunkerPrefetchChunk(t *testing.T) {
	size := ChunkSize * 4
	baseData := bytes.Repeat([]byte("H"), int(size))
	base := block.NewMockDevice(baseData, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), false)

	// Create a new Chunker
	ctx, cancel := context.WithCancel(context.Background())
	chunker := NewChunker(ctx, base, cache)
	defer cancel()

	// Prefetch a chunk
	_, err := chunker.ReadAt(nil, ChunkSize*2)
	require.NoError(t, err)

	// Read the prefetched chunk
	prefetchedBuf := make([]byte, block.Size)
	_, err = cache.ReadAt(prefetchedBuf, ChunkSize*2)
	require.NoError(t, err)

	// The check for total equality takes a really long time
	assert.EqualValues(t, baseData[ChunkSize*2], prefetchedBuf[0])
	assert.EqualValues(t, baseData[ChunkSize*3-1], prefetchedBuf[block.Size-1])
}

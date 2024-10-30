package source

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/e2b-dev/infra/packages/block-storage/pkg/block"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const blockSize = 4096

func generateRandomData(size int) ([]byte, error) {
	data := make([]byte, size)

	_, err := rand.Read(data)
	if err != nil {
		return nil, fmt.Errorf("error generating random data: %w", err)
	}

	return data, nil
}

func TestChunkerReadBlock(t *testing.T) {
	size := ChunkSize * 2

	baseData, err := generateRandomData(size)
	require.NoError(t, err)

	base := block.NewMockDevice(baseData, blockSize, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), blockSize, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunker := NewChunker(ctx, base, cache)

	// Read data from the Chunker
	offset := int64(0)
	readSize := blockSize
	buf := make([]byte, readSize)

	_, err = chunker.ReadAt(buf, offset)
	require.NoError(t, err)

	assert.True(t, bytes.Equal(baseData[offset:blockSize], buf))
}

func TestChunkerReadMultipleBlocks(t *testing.T) {
	size := ChunkSize * 10

	baseData, err := generateRandomData(size)
	require.NoError(t, err)

	base := block.NewMockDevice(baseData, blockSize, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), blockSize, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunker := NewChunker(ctx, base, cache)

	// Read data from the Chunker
	offset := blockSize
	readSize := blockSize * 2
	buf := make([]byte, readSize)

	_, err = chunker.ReadAt(buf, int64(offset))
	require.NoError(t, err)

	assert.True(t, bytes.Equal(baseData[offset:offset+readSize], buf))
}

func TestChunkerReadSmaller(t *testing.T) {
	size := ChunkSize * 3

	baseData, err := generateRandomData(size)
	require.NoError(t, err)

	base := block.NewMockDevice(baseData, blockSize, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), blockSize, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunker := NewChunker(ctx, base, cache)

	readSize := blockSize / 4
	offset := 10
	// Read a chunk of data from the Chunker
	chunkBuf := make([]byte, readSize)
	_, err = chunker.ReadAt(chunkBuf, int64(offset))
	require.NoError(t, err)

	assert.True(t, bytes.Equal(baseData[offset:offset+readSize], chunkBuf))
}

func TestChunkerReadBigger(t *testing.T) {
	size := ChunkSize * 5

	baseData, err := generateRandomData(size)
	require.NoError(t, err)

	base := block.NewMockDevice(baseData, blockSize, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), blockSize, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunker := NewChunker(ctx, base, cache)

	readSize := blockSize*2 + 20
	offset := 5
	// Read a chunk of data from the Chunker
	chunkBuf := make([]byte, readSize)
	_, err = chunker.ReadAt(chunkBuf, int64(offset))
	require.NoError(t, err)

	assert.True(t, bytes.Equal(baseData[offset:offset+readSize], chunkBuf))
}

func TestChunkerReadCache(t *testing.T) {
	size := ChunkSize * 4

	baseData, err := generateRandomData(size)
	require.NoError(t, err)

	base := block.NewMockDevice(baseData, blockSize, true)
	cache := block.NewMockDevice(make([]byte, len(baseData)), blockSize, false)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	chunker := NewChunker(ctx, base, cache)

	offset := ChunkSize
	readSize := blockSize

	// Prefetch a chunk
	_, err = chunker.ReadAt(nil, int64(offset))
	require.NoError(t, err)

	// Read the prefetched chunk
	prefetchedBuf := make([]byte, readSize)
	_, err = cache.ReadAt(prefetchedBuf, int64(offset))
	require.NoError(t, err)

	// The check for total equality takes a really long time
	assert.True(t, bytes.Equal(baseData[offset:offset+readSize], prefetchedBuf))
}

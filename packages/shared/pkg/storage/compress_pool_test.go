package storage

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompressLZ4_RoundTrip(t *testing.T) {
	t.Parallel()
	src := bytes.Repeat([]byte("hello world "), 1000)

	compressed, err := CompressLZ4(src)
	require.NoError(t, err)
	require.Less(t, len(compressed), len(src), "compressed should be smaller")

	decompressed, err := DecompressLZ4(compressed, make([]byte, len(src)))
	require.NoError(t, err)
	assert.Equal(t, src, decompressed)
}

func TestNewCompressorPool_LZ4(t *testing.T) {
	t.Parallel()
	borrow, release := newCompressorPool(CompressionLZ4, 0, 0, 0)

	c, err := borrow()
	require.NoError(t, err)
	defer release(c)

	src := bytes.Repeat([]byte("compress me "), 500)
	compressed, err := c.Compress(src)
	require.NoError(t, err)
	require.Less(t, len(compressed), len(src))

	decompressed, err := DecompressLZ4(compressed, make([]byte, len(src)))
	require.NoError(t, err)
	assert.Equal(t, src, decompressed)
}

func TestNewCompressorPool_Zstd(t *testing.T) {
	t.Parallel()
	borrow, release := newCompressorPool(CompressionZstd, 1, 0, 1)

	c, err := borrow()
	require.NoError(t, err)
	defer release(c)

	src := bytes.Repeat([]byte("zstd test data "), 500)
	compressed, err := c.Compress(src)
	require.NoError(t, err)
	require.Less(t, len(compressed), len(src))
}

func TestZstdDecoderPool(t *testing.T) {
	t.Parallel()
	src := bytes.Repeat([]byte("decoder pool test "), 500)

	borrow, release := newCompressorPool(CompressionZstd, 1, 0, 1)
	c, err := borrow()
	require.NoError(t, err)

	compressed, err := c.Compress(src)
	require.NoError(t, err)
	release(c)

	// Decode using the pool.
	dec, err := getZstdDecoder(bytes.NewReader(compressed))
	require.NoError(t, err)

	decompressed, err := io.ReadAll(dec)
	require.NoError(t, err)
	putZstdDecoder(dec)

	assert.Equal(t, src, decompressed)

	// Borrow again from pool to verify reuse works.
	dec2, err := getZstdDecoder(bytes.NewReader(compressed))
	require.NoError(t, err)

	decompressed2, err := io.ReadAll(dec2)
	require.NoError(t, err)
	putZstdDecoder(dec2)

	assert.Equal(t, src, decompressed2)
}

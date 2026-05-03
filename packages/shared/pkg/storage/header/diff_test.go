package header

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsZero(t *testing.T) {
	t.Parallel()

	zeroes := func(n int) []byte { return make([]byte, n) }

	assert.True(t, IsZero(nil))
	assert.True(t, IsZero(zeroes(0)))
	assert.True(t, IsZero(zeroes(1)))
	assert.True(t, IsZero(zeroes(3)))
	assert.True(t, IsZero(zeroes(RootfsBlockSize)))
	assert.True(t, IsZero(bytes.Repeat([]byte{0}, HugepageSize)))

	assert.False(t, IsZero([]byte{1}))
	assert.False(t, IsZero([]byte{0, 1, 0}), "middle byte non-zero")

	// Non-zero byte buried where the 3-sample short-circuit cannot see it
	// must still be caught by the SIMD fallback.
	buf := zeroes(RootfsBlockSize)
	buf[100] = 0xFF
	assert.False(t, IsZero(buf))

	buf = zeroes(RootfsBlockSize)
	buf[RootfsBlockSize-2] = 0xFF
	assert.False(t, IsZero(buf), "non-zero just before the last (sampled) byte")
}

func TestIsEmptyBlock(t *testing.T) {
	t.Parallel()

	ok, err := IsEmptyBlock(make([]byte, RootfsBlockSize), RootfsBlockSize)
	require.NoError(t, err)
	assert.True(t, ok)

	nonzero := make([]byte, RootfsBlockSize)
	nonzero[42] = 1
	ok, err = IsEmptyBlock(nonzero, RootfsBlockSize)
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = IsEmptyBlock(make([]byte, RootfsBlockSize), 1234)
	require.Error(t, err, "unsupported block size must error")

	_, err = IsEmptyBlock(make([]byte, 100), RootfsBlockSize)
	require.Error(t, err, "buffer length / block size mismatch must error")
}

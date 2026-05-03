package block

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsZero(t *testing.T) {
	t.Parallel()

	zeroes := func(n int) []byte { return make([]byte, n) }

	assert.True(t, IsZero(nil))
	assert.True(t, IsZero(zeroes(0)))
	assert.True(t, IsZero(zeroes(1)))
	assert.True(t, IsZero(zeroes(3)))
	assert.True(t, IsZero(zeroes(4096)))

	assert.False(t, IsZero([]byte{1}))
	assert.False(t, IsZero([]byte{0, 1, 0}), "middle byte non-zero")

	// Edge: a single non-zero byte buried in the interior must not be missed
	// by the 3-sample short-circuit. Use a length whose first/last/midpoint
	// are still zero so the SIMD fallback is what catches it.
	buf := zeroes(4096)
	buf[100] = 0xFF
	assert.False(t, IsZero(buf))

	buf = zeroes(4096)
	buf[4094] = 0xFF
	assert.False(t, IsZero(buf), "non-zero just before last byte")

	assert.True(t, IsZero(bytes.Repeat([]byte{0}, 1<<20)))
}

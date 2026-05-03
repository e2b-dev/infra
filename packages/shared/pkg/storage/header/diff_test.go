package header

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsZero(t *testing.T) {
	t.Parallel()

	assert.True(t, IsZero(make([]byte, RootfsBlockSize)))
	assert.False(t, IsZero([]byte{0, 1, 0}), "middle byte is sampled")

	// Non-zero byte buried where the 3-sample short-circuit cannot see it
	// must still be caught by the SIMD fallback.
	buf := make([]byte, RootfsBlockSize)
	buf[100] = 0xFF
	assert.False(t, IsZero(buf))

	buf = make([]byte, RootfsBlockSize)
	buf[RootfsBlockSize-2] = 0xFF
	assert.False(t, IsZero(buf), "non-zero just before the last sampled byte")
}

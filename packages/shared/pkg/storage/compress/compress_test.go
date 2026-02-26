package compress

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFrameSizeMatchesHugepage(t *testing.T) {
	assert.Equal(t, 2*1024*1024, FrameSize)
	assert.Equal(t, FrameSize, MaxFrameData)
}

func TestCompressDataRoundtrip(t *testing.T) {
	const size = 10 * 1024 * 1024
	data := make([]byte, size)
	rand.Read(data[0 : size*4/10])

	var compressed bytes.Buffer
	frames, err := CompressData(context.Background(), bytes.NewReader(data), &compressed, DefaultConfig())
	require.NoError(t, err)
	require.NotEmpty(t, frames)

	compSize := compressed.Len()
	t.Logf("compressed %d → %d bytes (%.1f:1)", size, compSize, float64(size)/float64(compSize))

	reader, err := NewReaderFromFrames(bytes.NewReader(compressed.Bytes()), frames)
	require.NoError(t, err)

	got := make([]byte, size)
	n, err := reader.ReadAt(got, 0)
	require.NoError(t, err)
	assert.Equal(t, size, n)
	assert.Equal(t, data, got)

	uncompSize, err := reader.Seek(0, io.SeekEnd)
	require.NoError(t, err)
	assert.Equal(t, int64(size), uncompSize)

	mid := make([]byte, 4096)
	n, err = reader.ReadAt(mid, FrameSize+1024)
	require.NoError(t, err)
	assert.Equal(t, 4096, n)
	assert.Equal(t, data[FrameSize+1024:FrameSize+1024+4096], mid)
}

func TestCompressRatio(t *testing.T) {
	tests := []struct {
		name     string
		gen      func([]byte)
		minRatio float64
	}{
		{"zeros", func(b []byte) {}, 100.0},
		{"random", func(b []byte) { rand.Read(b) }, 0.9},
		{"vm-like", func(b []byte) {
			for i := 0; i < len(b)/5; i++ {
				off := i * 5
				rand.Read(b[off : off+1])
			}
		}, 2.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const size = 8 * 1024 * 1024
			data := make([]byte, size)
			tt.gen(data)

			var compressed bytes.Buffer
			_, err := CompressData(context.Background(), bytes.NewReader(data), &compressed, DefaultConfig())
			require.NoError(t, err)

			ratio := float64(size) / float64(compressed.Len())
			t.Logf("%s: %d → %d bytes (%.1f:1)", tt.name, size, compressed.Len(), ratio)
			assert.GreaterOrEqual(t, ratio, tt.minRatio)
		})
	}
}

func TestConcurrentReadAt(t *testing.T) {
	const size = 8 * 1024 * 1024
	data := make([]byte, size)
	rand.Read(data)

	var compressed bytes.Buffer
	frames, err := CompressData(context.Background(), bytes.NewReader(data), &compressed, DefaultConfig())
	require.NoError(t, err)

	reader, err := NewReaderFromFrames(bytes.NewReader(compressed.Bytes()), frames)
	require.NoError(t, err)

	errs := make(chan error, 4)
	for i := range 4 {
		go func() {
			off := int64(i) * FrameSize
			buf := make([]byte, FrameSize)
			n, err := reader.ReadAt(buf, off)
			if err != nil {
				errs <- err
				return
			}
			if n != FrameSize {
				errs <- assert.AnError
				return
			}
			if !bytes.Equal(buf, data[off:off+FrameSize]) {
				errs <- assert.AnError
				return
			}
			errs <- nil
		}()
	}

	for range 4 {
		require.NoError(t, <-errs)
	}
}

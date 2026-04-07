package storage

import (
	"bytes"
	"io"
	"os"
	"testing"

	lz4 "github.com/pierrec/lz4/v4"
	"github.com/stretchr/testify/require"
)

// lz4Compress is a test helper that LZ4-compresses src.
func lz4Compress(t *testing.T, src []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := lz4.NewWriter(&buf)
	_, err := w.Write(src)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	return buf.Bytes()
}

func TestDecompressingCacheReader(t *testing.T) {
	t.Parallel()

	newTestCache := func(t *testing.T) cachedSeekable {
		t.Helper()

		return cachedSeekable{
			path:      t.TempDir(),
			chunkSize: 10,
			tracer:    noopTracer,
		}
	}

	original := []byte("the quick brown fox jumps over the lazy dog")
	compressed := lz4Compress(t, original)

	t.Run("complete read is cached", func(t *testing.T) {
		t.Parallel()

		c := newTestCache(t)
		framePath := makeFrameFilename(c.path, FrameOffset{C: 0}, FrameSize{C: int32(len(compressed))})

		rc, err := newDecompressingCacheReader(
			io.NopCloser(bytes.NewReader(compressed)),
			CompressionLZ4,
			len(compressed),
			&c, t.Context(), framePath, 0,
		)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, original, got)

		require.NoError(t, rc.Close())
		c.wg.Wait()

		cached, err := os.ReadFile(framePath)
		require.NoError(t, err)
		require.Equal(t, compressed, cached)
	})

	t.Run("wrong expectedSize returns error on Close", func(t *testing.T) {
		t.Parallel()

		c := newTestCache(t)
		framePath := makeFrameFilename(c.path, FrameOffset{C: 0}, FrameSize{C: int32(len(compressed))})

		rc, err := newDecompressingCacheReader(
			io.NopCloser(bytes.NewReader(compressed)),
			CompressionLZ4,
			len(compressed)+100, // wrong size
			&c, t.Context(), framePath, 0,
		)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, original, got, "decompressed data should be correct regardless")

		err = rc.Close()
		require.Error(t, err)
		require.Contains(t, err.Error(), "compressed frame cache writeback")

		c.wg.Wait()

		_, err = os.Stat(framePath)
		require.True(t, os.IsNotExist(err), "mismatched frame should not be cached")
	})
}

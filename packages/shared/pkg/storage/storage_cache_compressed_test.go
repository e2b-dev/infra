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

// lz4CompressProd matches the production encoder configuration (compress_encode.go):
// BlockSize=4Mb, BlockChecksumOption(true), ChecksumOption(false). Output ends in
// a 4-byte EndMark; with content checksum disabled, the decoder will not pull
// past the last block's data unless the caller reads past EOF.
func lz4CompressProd(t *testing.T, src []byte) []byte {
	t.Helper()

	var buf bytes.Buffer

	w := lz4.NewWriter(&buf)
	require.NoError(t, w.Apply(
		lz4.BlockSizeOption(lz4.Block4Mb),
		lz4.BlockChecksumOption(true),
		lz4.ChecksumOption(false),
	))
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
		framePath := makeFrameFilename(c.path, Range{Offset: 0, Length: len(compressed)})

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

	t.Run("io.ReadFull at exact uncompressed size still populates cache (production LZ4 options)", func(t *testing.T) {
		t.Parallel()

		// Mirror the chunker's progressiveRead: io.ReadFull with the EXACT
		// uncompressed byte count, against an encoder configured the way prod
		// configures it. With BlockChecksumOption(true)+ChecksumOption(false),
		// the trailing 4-byte EndMark is part of the encoded frame but lz4.Reader
		// does not pull it through the tee unless the caller reads past EOF.
		// The cache writeback path must tolerate that — failing the read for a
		// writeback short would mean every subsequent read for the same block
		// repeats the GCS round-trip and re-fails Close, defeating both cache
		// tiers (chunker mmap bitmap + NFS .frm).
		c := newTestCache(t)
		compressedProd := lz4CompressProd(t, original)
		framePath := makeFrameFilename(c.path, Range{Offset: 0, Length: len(compressedProd)})

		rc, err := newDecompressingCacheReader(
			io.NopCloser(bytes.NewReader(compressedProd)),
			CompressionLZ4,
			len(compressedProd),
			&c, t.Context(), framePath, 0,
		)
		require.NoError(t, err)

		out := make([]byte, len(original))
		n, err := io.ReadFull(rc, out)
		require.NoError(t, err)
		require.Equal(t, len(original), n)
		require.Equal(t, original, out)

		require.NoError(t, rc.Close(), "writeback failure must not surface as a read error")
		c.wg.Wait()

		_, err = os.Stat(framePath)
		require.NoError(t, err, "frame should be cached after a successful complete read")
	})

	t.Run("size mismatch skips cache writeback but does not fail the read", func(t *testing.T) {
		t.Parallel()

		c := newTestCache(t)
		framePath := makeFrameFilename(c.path, Range{Offset: 0, Length: len(compressed)})

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

		require.NoError(t, rc.Close(), "writeback failure must not surface as a read error")

		c.wg.Wait()

		_, err = os.Stat(framePath)
		require.True(t, os.IsNotExist(err), "mismatched frame should not be cached")
	})
}

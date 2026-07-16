package storage

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync/atomic"
	"testing"

	lz4 "github.com/pierrec/lz4/v4"
	"github.com/stretchr/testify/mock"
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

		var captured []byte
		capturing := newCaptureReader(bytesRangeReader(compressed), len(compressed), true,
			func(_ context.Context, frame []byte) { captured = frame })
		rc, err := NewDecompressReader(capturing, CompressionLZ4, SourceFS, c.objType)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, original, got)

		mustClose(t, rc)
		c.writeFrameBack(t.Context(), framePath, 0, len(compressed), SourceFS, CompressionLZ4, captured)
		c.wg.Wait()

		cached, err := os.ReadFile(framePath)
		require.NoError(t, err)
		require.Equal(t, compressed, cached)
	})

	t.Run("io.ReadFull at exact uncompressed size still populates cache (production LZ4 options)", func(t *testing.T) {
		t.Parallel()

		// Mirror the chunker's progressiveFetch: io.ReadFull with the EXACT
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

		var captured []byte
		capturing := newCaptureReader(bytesRangeReader(compressedProd), len(compressedProd), true,
			func(_ context.Context, frame []byte) { captured = frame })
		rc, err := NewDecompressReader(capturing, CompressionLZ4, SourceFS, c.objType)
		require.NoError(t, err)

		out := make([]byte, len(original))
		n, err := io.ReadFull(rc, out)
		require.NoError(t, err)
		require.Equal(t, len(original), n)
		require.Equal(t, original, out)

		_, closeErr := rc.Close(t.Context())
		require.NoError(t, closeErr, "close must not surface a read error")
		// drainOnClose captured the full frame even though the caller stopped
		// at the exact uncompressed size (never pulling the lz4 EndMark).
		c.writeFrameBack(t.Context(), framePath, 0, len(compressedProd), SourceFS, CompressionLZ4, captured)
		c.wg.Wait()

		_, err = os.Stat(framePath)
		require.NoError(t, err, "frame should be cached after a successful complete read")
	})

	t.Run("size mismatch skips cache writeback but does not fail the read", func(t *testing.T) {
		t.Parallel()

		c := newTestCache(t)
		framePath := makeFrameFilename(c.path, Range{Offset: 0, Length: len(compressed)})

		var captured []byte
		capturing := newCaptureReader(bytesRangeReader(compressed), len(compressed), true,
			func(_ context.Context, frame []byte) { captured = frame })
		rc, err := NewDecompressReader(capturing, CompressionLZ4, SourceFS, c.objType)
		require.NoError(t, err)

		got, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.Equal(t, original, got, "decompressed data should be correct regardless")

		_, closeErr := rc.Close(t.Context())
		require.NoError(t, closeErr, "close must not surface a read error")

		// Wrong expected size (larger than the captured frame) -> short -> skip.
		c.writeFrameBack(t.Context(), framePath, 0, len(compressed)+100, SourceFS, CompressionLZ4, captured)
		c.wg.Wait()

		_, err = os.Stat(framePath)
		require.True(t, os.IsNotExist(err), "mismatched frame should not be cached")
	})
}

// TestCorruptFetchPoisonsCacheRecovers guards the compressed cache against
// poisoning: a fetch that returns corrupt bytes at the right length passes the
// size-only writeback guard, so it must not be cached (and a cache hit that
// fails to decode must be evicted). After the upstream heals the read
// recovers, rather than failing forever.
func TestCorruptFetchPoisonsCacheRecovers(t *testing.T) {
	t.Parallel()

	data := generateSemiRandomData(1 * megabyte) // single frame
	up := &memPartUploader{}
	fullFT, _, err := compressStream(t.Context(), bytes.NewReader(data), defaultCfg(CompressionZstd, 2, 2*megabyte), up, 2, nil)
	require.NoError(t, err)
	blob := up.Assemble()

	corrupt := bytes.Clone(blob)
	corrupt[len(corrupt)/2] ^= 0xFF

	var serveCorrupt atomic.Bool
	serveCorrupt.Store(true)

	inner := NewMockSeekable(t)
	inner.EXPECT().OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *FrameTable) (RangeReader, Source, error) {
			b := blob
			if serveCorrupt.Load() {
				b = corrupt
			}

			return bytesRangeReader(b[off : off+length]), SourceAWS, nil
		}).Maybe()

	c := cachedSeekable{path: t.TempDir(), inner: inner, tracer: noopTracer, chunkSize: 1024}
	ft := fullFT.Table()

	// First read: corrupt upstream -> decode error; the corrupt frame must
	// not be cached.
	rr, _, err := c.OpenRangeReader(t.Context(), 0, 0, ft)
	require.NoError(t, err)
	_, readErr := io.Copy(io.Discard, rr)
	rr.Close(t.Context())
	require.Error(t, readErr)
	c.wg.Wait()

	// Upstream heals; the read must now succeed (refetch, not a poisoned hit).
	serveCorrupt.Store(false)
	rr2, _, err := c.OpenRangeReader(t.Context(), 0, 0, ft)
	require.NoError(t, err)
	var got bytes.Buffer
	_, readErr2 := got.ReadFrom(rr2)
	rr2.Close(t.Context())
	c.wg.Wait()
	require.NoError(t, readErr2, "read must recover after upstream heals (cache not poisoned)")
	require.Equal(t, data, got.Bytes())
}

// TestTruncatedFetchNotCached verifies a short fetch is not cached and the
// next read recovers once the upstream heals.
func TestTruncatedFetchNotCached(t *testing.T) {
	t.Parallel()

	data := generateSemiRandomData(1 * megabyte)
	up := &memPartUploader{}
	fullFT, _, err := compressStream(t.Context(), bytes.NewReader(data), defaultCfg(CompressionZstd, 2, 2*megabyte), up, 2, nil)
	require.NoError(t, err)
	blob := up.Assemble()

	var serveTruncated atomic.Bool
	serveTruncated.Store(true)

	inner := NewMockSeekable(t)
	inner.EXPECT().OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *FrameTable) (RangeReader, Source, error) {
			b := blob[off : off+length]
			if serveTruncated.Load() {
				b = b[:len(b)/2]
			}

			return bytesRangeReader(b), SourceAWS, nil
		}).Maybe()

	c := cachedSeekable{path: t.TempDir(), inner: inner, tracer: noopTracer, chunkSize: 1024}
	ft := fullFT.Table()

	rr, _, err := c.OpenRangeReader(t.Context(), 0, 0, ft)
	require.NoError(t, err)
	_, readErr := io.Copy(io.Discard, rr)
	rr.Close(t.Context())
	require.Error(t, readErr)
	c.wg.Wait()

	frameFile := makeFrameFilename(c.path, Range{Offset: 0, Length: int(ft.CompressedSize())})
	_, statErr := os.Stat(frameFile)
	require.True(t, os.IsNotExist(statErr), "truncated frame must not be cached")

	serveTruncated.Store(false)
	rr2, _, err := c.OpenRangeReader(t.Context(), 0, 0, ft)
	require.NoError(t, err)
	var got bytes.Buffer
	_, readErr2 := got.ReadFrom(rr2)
	rr2.Close(t.Context())
	c.wg.Wait()
	require.NoError(t, readErr2, "read must recover after upstream heals")
	require.Equal(t, data, got.Bytes())
}

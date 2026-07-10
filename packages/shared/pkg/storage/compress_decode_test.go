package storage

import (
	"bytes"
	"crypto/sha256"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDecompressReaderVerifiesCRCOnClose ensures a footer-corrupt frame is not
// reported as success when the caller reads exactly the uncompressed size.
// zstd only verifies the frame checksum once the footer is consumed, which an
// exact-size read never triggers, so Close must drain-verify and surface the
// error.
func TestDecompressReaderVerifiesCRCOnClose(t *testing.T) {
	t.Parallel()

	const frameU = 512 * 1024
	data := generateSemiRandomData(frameU)

	up := &memPartUploader{}
	fullFT, _, err := compressStream(t.Context(), bytes.NewReader(data), defaultCfg(CompressionZstd, 1, frameU), up, 1, nil)
	require.NoError(t, err)
	require.Equal(t, 1, fullFT.Table().NumFrames())
	blob := up.Assemble()

	// Corrupt the frame footer (last byte) so only the trailing checksum,
	// verified after the last content byte, is wrong.
	corrupt := bytes.Clone(blob)
	corrupt[len(corrupt)-1] ^= 0xFF

	dec, err := NewDecompressReader(bytesRangeReader(corrupt), CompressionZstd, SourceAWS, SeekableObjectType(0))
	require.NoError(t, err)

	// Read EXACTLY the uncompressed frame size — no read past EOF.
	buf := make([]byte, frameU)
	_, _ = io.ReadFull(dec, buf)
	_, closeErr := dec.Close(t.Context())
	require.Error(t, closeErr, "exact-size read of a footer-corrupt frame must fail on Close")

	// A clean frame still round-trips and closes without error.
	dec2, err := NewDecompressReader(bytesRangeReader(blob), CompressionZstd, SourceAWS, SeekableObjectType(0))
	require.NoError(t, err)
	got := make([]byte, frameU)
	_, err = io.ReadFull(dec2, got)
	require.NoError(t, err)
	_, closeErr = dec2.Close(t.Context())
	require.NoError(t, closeErr)
	require.Equal(t, sha256.Sum256(data), sha256.Sum256(got))
}

package storage

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

// helper: make a rangeRead that serves data from a byte slice.
func rangeReadFrom(data []byte) RangeReadFunc {
	return func(_ context.Context, offset int64, length int) (io.ReadCloser, error) {
		end := min(offset+int64(length), int64(len(data)))

		return io.NopCloser(bytes.NewReader(data[offset:end])), nil
	}
}

func compressTestData(t *testing.T, data []byte, typ string) (*FrameTable, []byte) {
	t.Helper()
	cfg := &CompressConfig{
		Enabled:            true,
		Type:               typ,
		Level:              1,
		FrameSizeKB:        32,
		FrameEncodeWorkers: 1,
		EncoderConcurrency: 1,
	}
	ft, compressed, _, err := CompressBytes(context.Background(), data, cfg)
	require.NoError(t, err)

	return ft, compressed
}

func TestReadFrame_CompressedPassthrough(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Create repeatable test data (one frame worth).
	const frameKB = 32
	original := bytes.Repeat([]byte("ABCDEFGH"), frameKB*1024/8)

	ft, compressed := compressTestData(t, original, "zstd")

	// Read with decompress=false: should get raw compressed bytes.
	frameStart, frameSize, err := ft.FrameFor(0)
	require.NoError(t, err)
	_ = frameStart

	buf := make([]byte, int(frameSize.C))
	r, err := ReadFrame(ctx, rangeReadFrom(compressed), "test", 0, ft, false, buf, int64(len(buf)), nil)
	require.NoError(t, err)
	require.Equal(t, int(frameSize.C), r.Length)
	require.Equal(t, compressed[:frameSize.C], buf[:r.Length])
}

func TestReadFrame_BufferTooSmall(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const frameKB = 32
	original := bytes.Repeat([]byte("ABCDEFGH"), frameKB*1024/8)
	ft, compressed := compressTestData(t, original, "zstd")

	// Buffer smaller than the uncompressed frame size.
	buf := make([]byte, 16)
	_, err := ReadFrame(ctx, rangeReadFrom(compressed), "test", 0, ft, true, buf, int64(len(buf)), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "buffer too small")
}

func TestReadFrame_LZ4Decompression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const frameKB = 32
	original := bytes.Repeat([]byte("LZ4TEST!"), frameKB*1024/8)

	ft, compressed := compressTestData(t, original, "lz4")

	buf := make([]byte, frameKB*1024)
	r, err := ReadFrame(ctx, rangeReadFrom(compressed), "test", 0, ft, true, buf, int64(len(buf)), nil)
	require.NoError(t, err)
	require.Equal(t, len(original), r.Length)
	require.Equal(t, original, buf[:r.Length])
}

func TestReadFrame_ShortRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Uncompressed path: rangeRead returns fewer bytes than buffer size.
	original := []byte("hello world")
	buf := make([]byte, 64) // larger than data

	// rangeRead returns only len(original) bytes, but ReadFrame expects len(buf).
	rangeRead := func(_ context.Context, _ int64, _ int) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(original)), nil
	}

	_, err := ReadFrame(ctx, rangeRead, "test-short", 0, nil, false, buf, int64(len(buf)), nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "incomplete ReadFrame")
}

func TestReadFrame_OnReadNil_Uncompressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	data := bytes.Repeat([]byte("X"), 256)
	buf := make([]byte, len(data))

	r, err := ReadFrame(ctx, rangeReadFrom(data), "test", 0, nil, false, buf, int64(len(buf)), nil)
	require.NoError(t, err)
	require.Equal(t, len(data), r.Length)
	require.Equal(t, data, buf[:r.Length])
}

func TestReadFrame_OnReadNil_Compressed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const frameKB = 32
	original := bytes.Repeat([]byte("NILTEST!"), frameKB*1024/8)

	ft, compressed := compressTestData(t, original, "zstd")

	buf := make([]byte, frameKB*1024)
	r, err := ReadFrame(ctx, rangeReadFrom(compressed), "test", 0, ft, true, buf, int64(len(buf)), nil)
	require.NoError(t, err)
	require.Equal(t, len(original), r.Length)
	require.Equal(t, original, buf[:r.Length])
}

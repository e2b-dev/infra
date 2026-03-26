package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// generateSemiRandomData produces deterministic, compressible data.
// Random byte repeated 1-16 times — gives ~0.5-0.7 compression ratio.
func generateSemiRandomData(size int) []byte {
	data := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic
	i := 0
	for i < size {
		runLen := rng.IntN(16) + 1
		if i+runLen > size {
			runLen = size - i
		}
		b := byte(rng.IntN(256))
		for j := range runLen {
			data[i+j] = b
		}
		i += runLen
	}

	return data
}

// ThrottledPartUploader wraps memPartUploader with simulated upload bandwidth.
type ThrottledPartUploader struct {
	memPartUploader

	bandwidth int64 // bytes/sec; 0 = unlimited
}

func (t *ThrottledPartUploader) UploadPart(ctx context.Context, partIndex int, data ...[]byte) error {
	if t.bandwidth > 0 {
		total := 0
		for _, d := range data {
			total += len(d)
		}
		time.Sleep(time.Duration(float64(total) / float64(t.bandwidth) * float64(time.Second)))
	}

	return t.memPartUploader.UploadPart(ctx, partIndex, data...)
}

// decompressAll walks the FrameTable and decompresses each frame from the
// concatenated compressed blob, returning the original uncompressed data.
func decompressAll(ft *FrameTable, compressed []byte) ([]byte, error) {
	var result []byte
	var cOff int64

	for i, fs := range ft.Frames {
		if cOff+int64(fs.C) > int64(len(compressed)) {
			return nil, fmt.Errorf("frame %d: compressed data truncated (need %d, have %d)", i, cOff+int64(fs.C), len(compressed))
		}

		frameData := compressed[cOff : cOff+int64(fs.C)]
		var frame []byte
		var err error

		switch ft.CompressionType() {
		case CompressionLZ4:
			frame, err = DecompressLZ4(frameData, make([]byte, fs.U))
		case CompressionZstd:
			dec, derr := getZstdDecoder(bytes.NewReader(frameData))
			if derr != nil {
				return nil, fmt.Errorf("frame %d: zstd reader: %w", i, derr)
			}
			frame = make([]byte, fs.U)
			_, err = io.ReadFull(dec, frame)
			putZstdDecoder(dec)
		}

		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		result = append(result, frame...)
		cOff += int64(fs.C)
	}

	return result, nil
}

// defaultCfg returns a CompressConfig with the given overrides applied.
func defaultCfg(ct CompressionType, workers, frameSize int) *CompressConfig {
	level := 2 // zstd default
	if ct == CompressionLZ4 {
		level = 0
	}

	return &CompressConfig{
		Enabled:            true,
		Type:               ct.String(),
		Level:              level,
		EncoderConcurrency: 1,
		FrameEncodeWorkers: workers,
		FrameSizeKB:        frameSize / 1024,
		TargetPartSizeMB:   50,
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamRoundTrip
// ---------------------------------------------------------------------------

func TestCompressStreamRoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dataSize  int
		frameSize int
		workers   int
		codec     CompressionType
	}{
		{"basic", 10 * megabyte, 2 * megabyte, 4, CompressionZstd},
		{"workers_1", 10 * megabyte, 2 * megabyte, 1, CompressionZstd},
		{"workers_2", 10 * megabyte, 2 * megabyte, 2, CompressionZstd},
		{"not_frame_aligned", 10*megabyte + 1, 2 * megabyte, 4, CompressionZstd},
		{"smaller_than_frame", 100 * 1024, 2 * megabyte, 4, CompressionZstd},
		{"smaller_than_part", 5 * megabyte, 2 * megabyte, 4, CompressionZstd},
		{"empty", 0, 2 * megabyte, 4, CompressionZstd},
		{"single_byte", 1, 2 * megabyte, 1, CompressionZstd},
		{"lz4", 10 * megabyte, 2 * megabyte, 4, CompressionLZ4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var original []byte
			if tc.dataSize > 0 {
				original = generateSemiRandomData(tc.dataSize)
			}

			up := &memPartUploader{}
			cfg := defaultCfg(tc.codec, tc.workers, tc.frameSize)

			ft, checksum, err := compressStream(
				context.Background(),
				bytes.NewReader(original),
				cfg,
				up,
				4,
			)
			require.NoError(t, err)

			if tc.dataSize == 0 {
				assert.Empty(t, ft.Frames)
				assert.Equal(t, sha256.Sum256(nil), checksum)

				return
			}

			// Verify frame count.
			expectedFrames := (tc.dataSize + tc.frameSize - 1) / tc.frameSize
			assert.Len(t, ft.Frames, expectedFrames)

			// Verify checksum.
			assert.Equal(t, sha256.Sum256(original), checksum)

			// Round-trip: decompress and compare.
			compressed := up.Assemble()
			decompressed, err := decompressAll(ft, compressed)
			require.NoError(t, err)
			require.Equal(t, original, decompressed)
		})
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamContextCancel
// ---------------------------------------------------------------------------

func TestCompressStreamContextCancel(t *testing.T) {
	t.Parallel()

	data := generateSemiRandomData(100 * megabyte)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	up := &memPartUploader{}
	cfg := defaultCfg(CompressionZstd, 4, 2*megabyte)

	_, _, err := compressStream(ctx, bytes.NewReader(data), cfg, up, 4)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// TestCompressStreamPartCount
// ---------------------------------------------------------------------------

func TestCompressStreamPartSizeMinimum(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		dataSize         int
		frameSize        int
		targetPartSizeMB int
	}{
		{"large_file", 100 * megabyte, 2 * megabyte, 50},
		{"small_file_one_part", 5 * megabyte, 2 * megabyte, 50},
		{"small_target", 100 * megabyte, 2 * megabyte, 10},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := generateSemiRandomData(tc.dataSize)
			up := &memPartUploader{}
			cfg := defaultCfg(CompressionZstd, 4, tc.frameSize)
			cfg.TargetPartSizeMB = tc.targetPartSizeMB

			_, _, err := compressStream(context.Background(), bytes.NewReader(data), cfg, up, 4)
			require.NoError(t, err)

			// Verify: no non-final part is under 5 MiB.
			keys := make([]int, 0, len(up.parts))
			for k := range up.parts {
				keys = append(keys, k)
			}
			slices.Sort(keys)

			for i, k := range keys {
				isFinal := i == len(keys)-1
				if !isFinal {
					assert.GreaterOrEqual(t, len(up.parts[k]), 5*1024*1024,
						"non-final part %d is under 5 MiB (%d bytes)", k, len(up.parts[k]))
				}
			}

			assert.NotEmpty(t, up.parts, "should have at least one part")
		})
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamRace
// ---------------------------------------------------------------------------

// TestCompressStreamRace runs many concurrent CompressStream calls with high
// worker counts to shake out data races in the compressor pool, memPartUploader,
// and errgroup coordination. Run with -race.
func TestCompressStreamRace(t *testing.T) {
	t.Parallel()

	const (
		streams          = 8            // concurrent CompressStream calls
		dataSize         = 4 * megabyte // small enough to be fast, big enough to exercise batching
		frameSize        = 128 * 1024   // 128 KB — many frames per part
		workers          = 8            // high worker count to maximise contention
		targetPartSizeMB = 1            // small parts → many parts per stream
	)

	data := generateSemiRandomData(dataSize)
	wantChecksum := sha256.Sum256(data)

	// Use an errgroup to run all streams concurrently.
	eg, ctx := errgroup.WithContext(context.Background())
	for i := range streams {
		codec := CompressionZstd
		if i%2 == 1 {
			codec = CompressionLZ4 // mix codecs for more coverage
		}

		eg.Go(func() error {
			up := &memPartUploader{}
			cfg := defaultCfg(codec, workers, frameSize)
			cfg.TargetPartSizeMB = targetPartSizeMB
			if codec == CompressionZstd {
				cfg.EncoderConcurrency = 4 // multi-threaded zstd encoders for more contention
			}

			ft, checksum, err := compressStream(ctx, bytes.NewReader(data), cfg, up, 4)
			if err != nil {
				return fmt.Errorf("stream %d: compress: %w", i, err)
			}

			if checksum != wantChecksum {
				return fmt.Errorf("stream %d: checksum mismatch", i)
			}

			decompressed, err := decompressAll(ft, up.Assemble())
			if err != nil {
				return fmt.Errorf("stream %d: decompress: %w", i, err)
			}

			if !bytes.Equal(data, decompressed) {
				return fmt.Errorf("stream %d: round-trip data mismatch", i)
			}

			return nil
		})
	}

	require.NoError(t, eg.Wait())
}

// ---------------------------------------------------------------------------
// BenchmarkCompressStream
// ---------------------------------------------------------------------------

func BenchmarkCompress(b *testing.B) {
	const dataSize = 256 * megabyte
	data := generateSemiRandomData(dataSize)

	configs := []struct {
		name      string
		workers   int
		bandwidth int64 // bytes/sec; 0 = unlimited
	}{
		{"w1_unlimited", 1, 0},
		{"w2_unlimited", 2, 0},
		{"w4_unlimited", 4, 0},
		{"w1_200MBs", 1, 200 * megabyte},
		{"w4_200MBs", 4, 200 * megabyte},
		{"w4_100MBs", 4, 100 * megabyte},
	}

	for _, bcfg := range configs {
		b.Run(bcfg.name, func(b *testing.B) {
			compCfg := &CompressConfig{
				Enabled:            true,
				Type:               "zstd",
				Level:              2,
				EncoderConcurrency: 1,
				FrameEncodeWorkers: bcfg.workers,
				FrameSizeKB:        2 * 1024,
				TargetPartSizeMB:   50,
			}

			var lastParts atomic.Int32

			b.ResetTimer()
			b.SetBytes(int64(dataSize))

			for range b.N {
				up := &ThrottledPartUploader{bandwidth: bcfg.bandwidth}

				ft, _, err := compressStream(
					context.Background(),
					bytes.NewReader(data),
					compCfg,
					up, 4,
				)
				if err != nil {
					b.Fatal(err)
				}

				uSize, cSize := ft.Size()
				lastParts.Store(int32(len(up.parts)))

				_ = uSize
				_ = cSize
			}

			// Report after all iterations using last run's values.
			// b.SetBytes already reports MB/s (uncompressed throughput).
			b.ReportMetric(float64(lastParts.Load()), "parts")
		})
	}
}

package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
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

// ThrottledPartUploader wraps MemPartUploader with simulated upload bandwidth.
type ThrottledPartUploader struct {
	MemPartUploader
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

	return t.MemPartUploader.UploadPart(ctx, partIndex, data...)
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

		frame, err := DecompressFrame(ft.CompressionType, compressed[cOff:cOff+int64(fs.C)], fs.U)
		if err != nil {
			return nil, fmt.Errorf("frame %d: %w", i, err)
		}
		result = append(result, frame...)
		cOff += int64(fs.C)
	}

	return result, nil
}

// defaultOpts returns FramedUploadOptions with the given overrides applied.
func defaultOpts(ct CompressionType, workers, frameSize int) *FramedUploadOptions {
	level := 2 // zstd default
	if ct == CompressionLZ4 {
		level = 0
	}

	return &FramedUploadOptions{
		CompressionType:    ct,
		CompressionLevel:              level,
		EncoderConcurrency: 1,
		FrameEncodeWorkers:      workers,
		FrameSize:          frameSize,
		FramesPerUploadPart: 25,
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamRoundTrip
// ---------------------------------------------------------------------------

func TestCompressStreamRoundTrip(t *testing.T) {
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
			var original []byte
			if tc.dataSize > 0 {
				original = generateSemiRandomData(tc.dataSize)
			}

			up := &MemPartUploader{}
			opts := defaultOpts(tc.codec, tc.workers, tc.frameSize)

			ft, checksum, err := CompressStream(
				context.Background(),
				bytes.NewReader(original),
				opts,
				up,
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
// TestCompressStreamOnFrameReady
// ---------------------------------------------------------------------------

func TestCompressStreamOnFrameReady(t *testing.T) {
	data := generateSemiRandomData(10 * megabyte)

	type record struct {
		offset  FrameOffset
		size    FrameSize
		dataLen int
	}

	var records []record
	opts := defaultOpts(CompressionZstd, 4, 2*megabyte)
	opts.OnFrameReady = func(offset FrameOffset, size FrameSize, d []byte) error {
		records = append(records, record{offset: offset, size: size, dataLen: len(d)})

		return nil
	}

	up := &MemPartUploader{}
	ft, _, err := CompressStream(context.Background(), bytes.NewReader(data), opts, up)
	require.NoError(t, err)

	require.Len(t, records, len(ft.Frames))

	var expectU, expectC int64
	for i, r := range records {
		assert.Equal(t, expectU, r.offset.U, "frame %d: U offset", i)
		assert.Equal(t, expectC, r.offset.C, "frame %d: C offset", i)
		assert.Equal(t, int(r.size.C), r.dataLen, "frame %d: data len", i)
		expectU += int64(r.size.U)
		expectC += int64(r.size.C)
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamContextCancel
// ---------------------------------------------------------------------------

func TestCompressStreamContextCancel(t *testing.T) {
	data := generateSemiRandomData(100 * megabyte)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	up := &MemPartUploader{}
	opts := defaultOpts(CompressionZstd, 4, 2*megabyte)

	_, _, err := CompressStream(ctx, bytes.NewReader(data), opts, up)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// TestCompressStreamPartCount
// ---------------------------------------------------------------------------

func TestCompressStreamPartCount(t *testing.T) {
	tests := []struct {
		name              string
		dataSize          int
		frameSize         int
		framesPerPart     int
		expectedParts     int
	}{
		// 100MB / 2MB = 50 frames. 50 / 25 = 2 parts.
		{"two_parts", 100 * megabyte, 2 * megabyte, 25, 2},
		// 5MB / 2MB = 3 frames. 3 < 25 → 1 part.
		{"one_part_small", 5 * megabyte, 2 * megabyte, 25, 1},
		// 50MB / 2MB = 25 frames. 25 / 25 = 1 part exactly.
		{"exact_fit", 50 * megabyte, 2 * megabyte, 25, 1},
		// 51MB → 26 frames. 26 / 25 → 2 parts.
		{"just_over", 51 * megabyte, 2 * megabyte, 25, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := generateSemiRandomData(tc.dataSize)
			up := &MemPartUploader{}
			opts := defaultOpts(CompressionZstd, 4, tc.frameSize)
			opts.FramesPerUploadPart = tc.framesPerPart

			_, _, err := CompressStream(context.Background(), bytes.NewReader(data), opts, up)
			require.NoError(t, err)

			assert.Equal(t, tc.expectedParts, len(up.parts), "part count")
		})
	}
}

// ---------------------------------------------------------------------------
// TestCompressStreamRace
// ---------------------------------------------------------------------------

// TestCompressStreamRace runs many concurrent CompressStream calls with high
// worker counts to shake out data races in the compressor pool, MemPartUploader,
// and errgroup coordination. Run with -race.
func TestCompressStreamRace(t *testing.T) {
	const (
		streams       = 8            // concurrent CompressStream calls
		dataSize      = 4 * megabyte // small enough to be fast, big enough to exercise batching
		frameSize     = 128 * 1024   // 128 KB — many frames per part
		workers       = 8            // high worker count to maximise contention
		framesPerPart = 4            // small parts → many parts per stream
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
			up := &MemPartUploader{}
			opts := defaultOpts(codec, workers, frameSize)
			opts.FramesPerUploadPart = framesPerPart
			if codec == CompressionZstd {
				opts.EncoderConcurrency = 4 // multi-threaded zstd encoders for more contention
			}

			ft, checksum, err := CompressStream(ctx, bytes.NewReader(data), opts, up)
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

func BenchmarkCompressStream(b *testing.B) {
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

	for _, cfg := range configs {
		b.Run(cfg.name, func(b *testing.B) {
			opts := &FramedUploadOptions{
				CompressionType:    CompressionZstd,
				CompressionLevel:              2,
				EncoderConcurrency: 1,
				FrameEncodeWorkers:      cfg.workers,
				FrameSize:          2 * megabyte,
				FramesPerUploadPart: 25,
			}

			var lastParts atomic.Int32

			b.ResetTimer()
			b.SetBytes(int64(dataSize))

			for range b.N {
				up := &ThrottledPartUploader{bandwidth: cfg.bandwidth}

				ft, _, err := CompressStream(
					context.Background(),
					bytes.NewReader(data),
					opts,
					up,
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

// ---------------------------------------------------------------------------
// BenchmarkStoreFile — FS-backed StoreFile with workers × encoderConcurrency matrix
// ---------------------------------------------------------------------------

func BenchmarkStoreFile(b *testing.B) {
	const dataSize = 1024 * megabyte // 1 GB

	// Write input data to a temp file (once, shared across sub-benchmarks).
	data := generateSemiRandomData(dataSize)
	inputDir := b.TempDir()
	inputPath := filepath.Join(inputDir, "input.bin")
	require.NoError(b, os.WriteFile(inputPath, data, 0o644))
	data = nil // free memory, StoreFile reads from disk

	codecs := []struct {
		name  string
		codec CompressionType
		level int
	}{
		{"zstd1", CompressionZstd, 1},
		{"zstd2", CompressionZstd, 2},
		{"zstd3", CompressionZstd, 3},
		{"lz4", CompressionLZ4, 0},
	}
	workerCounts := []int{1, 2, 4, 8}

	for _, codec := range codecs {
		for _, workers := range workerCounts {
			name := fmt.Sprintf("%s/w%d", codec.name, workers)
			b.Run(name, func(b *testing.B) {
				opts := &FramedUploadOptions{
					CompressionType:     codec.codec,
					CompressionLevel:    codec.level,
					EncoderConcurrency:  1,
					FrameEncodeWorkers:  workers,
					FrameSize:           2 * megabyte,
					FramesPerUploadPart: 25,
				}

				b.SetBytes(int64(dataSize))
				b.ResetTimer()

				for range b.N {
					outDir := b.TempDir()
					outPath := filepath.Join(outDir, "output.dat")
					obj := &fsObject{path: outPath}

					ft, _, err := obj.StoreFile(b.Context(), inputPath, opts)
					if err != nil {
						b.Fatal(err)
					}

					uSize, cSize := ft.Size()
					b.ReportMetric(float64(cSize)/float64(uSize), "ratio")
				}
			})
		}
	}

	// Uncompressed baseline: raw file copy (read + write, no compression).
	b.Run("uncompressed", func(b *testing.B) {
		b.SetBytes(int64(dataSize))
		b.ResetTimer()

		for range b.N {
			outDir := b.TempDir()
			outPath := filepath.Join(outDir, "output.dat")

			in, err := os.Open(inputPath)
			if err != nil {
				b.Fatal(err)
			}
			out, err := os.Create(outPath)
			if err != nil {
				in.Close()
				b.Fatal(err)
			}
			if _, err := io.Copy(out, in); err != nil {
				in.Close()
				out.Close()
				b.Fatal(err)
			}
			in.Close()
			out.Close()
		}
	})
}

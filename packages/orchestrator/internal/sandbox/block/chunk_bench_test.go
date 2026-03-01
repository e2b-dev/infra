package block

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// --- Benchmark dimensions ---------------------------------------------------

const (
	megabyte      = 1024 * 1024
	benchDataSize = 100 * megabyte
	benchWorkers  = 4
)

var benchBlockSizes = []int64{
	4 * 1024,     // 4 KB — typical VM page fault
	2 * megabyte, // 2 MB — hugepage / sequential read
}

type backendProfile struct {
	name      string
	ttfb      time.Duration
	bandwidth int64 // bytes/sec
}

var profiles = []backendProfile{
	{name: "GCS", ttfb: 50 * time.Millisecond, bandwidth: 100 * megabyte},
	{name: "NFS", ttfb: 1 * time.Millisecond, bandwidth: 500 * megabyte},
}

var benchCodecs = []struct {
	name            string
	compressionType storage.CompressionType
	level           int
	frameSize       int
}{
	{name: "LZ4/2MB", compressionType: storage.CompressionLZ4, level: 0, frameSize: 2 * megabyte},
	{name: "Zstd1/2MB", compressionType: storage.CompressionZstd, level: 1, frameSize: 2 * megabyte},
	{name: "Zstd2/2MB", compressionType: storage.CompressionZstd, level: 2, frameSize: 2 * megabyte},
	{name: "Zstd3/2MB", compressionType: storage.CompressionZstd, level: 3, frameSize: 2 * megabyte},
}

// --- Setup helpers ----------------------------------------------------------

type benchReadF func(ctx context.Context, off, length int64) ([]byte, error)

type coldSetup struct {
	read       benchReadF
	close      func()
	fetchCount func() int64
	storeBytes int64 // compressed bytes per iteration (= benchDataSize for uncompressed)
}

// coldSetupF creates a fresh coldSetup for the Nth iteration (cold cache needs
// to be reinitialized every time).
type coldSetupF func(tb testing.TB, profile backendProfile, blockSize int64) coldSetup

func generateSemiRandomData(size int) []byte {
	data := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic
	// Random byte repeated 1–16 times.
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

func shuffledOffsets(dataSize, blockSize int64) []int64 {
	n := (dataSize + blockSize - 1) / blockSize
	offsets := make([]int64, n)
	for i := range offsets {
		offsets[i] = int64(i) * blockSize
	}
	rng := rand.New(rand.NewPCG(42, 99)) //nolint:gosec // deterministic
	rng.Shuffle(len(offsets), func(i, j int) { offsets[i], offsets[j] = offsets[j], offsets[i] })

	return offsets
}

func fmtSize(n int64) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%dMB", n/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%dKB", n/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func frameTableCompressedSize(ft *storage.FrameTable) int64 {
	var total int64
	for _, f := range ft.Frames {
		total += int64(f.C)
	}

	return total
}

// newColdSetup creates a coldSetupF for any chunker variant. For compressed
// runs, pass the pre-compressed data and frame table; for uncompressed/legacy
// pass nil for both.
func newColdSetup(data []byte, dataSize int64, ft *storage.FrameTable, compressedData []byte, legacy bool) coldSetupF {
	storeBytes := dataSize
	if ft != nil {
		storeBytes = frameTableCompressedSize(ft)
	}

	return func(tb testing.TB, profile backendProfile, blockSize int64) coldSetup {
		tb.Helper()

		src := data
		if compressedData != nil {
			src = compressedData
		}

		getter := &slowFrameGetter{data: src, ttfb: profile.ttfb, bandwidth: profile.bandwidth}

		if legacy {
			c, err := newFullFetchChunker(dataSize, blockSize, getter, tb.TempDir()+"/cache", newTestMetrics(tb))
			require.NoError(tb, err)

			return coldSetup{
				read:       func(ctx context.Context, off, length int64) ([]byte, error) { return c.Slice(ctx, off, length) },
				close:      func() { c.Close() },
				fetchCount: func() int64 { return getter.fetchCount.Load() },
				storeBytes: storeBytes,
			}
		}

		c, err := NewChunker(getter, dataSize, blockSize, tb.TempDir()+"/cache", newTestMetrics(tb))
		require.NoError(tb, err)

		return coldSetup{
			read:       func(ctx context.Context, off, length int64) ([]byte, error) { return c.GetBlock(ctx, off, length, ft) },
			close:      func() { c.Close() },
			fetchCount: func() int64 { return getter.fetchCount.Load() },
			storeBytes: storeBytes,
		}
	}
}

// runCold benchmarks cold-cache concurrent reads. Each b.N iteration creates
// a fresh cache and reads all offsets concurrently with benchWorkers goroutines.
func runCold(b *testing.B, dataSize, blockSize int64, profile backendProfile, newIter coldSetupF) {
	b.Helper()

	offsets := shuffledOffsets(dataSize, blockSize)
	b.ResetTimer()

	var totalElapsed time.Duration
	var storeBytes int64

	for range b.N {
		b.StopTimer()
		s := newIter(b, profile, blockSize)
		storeBytes = s.storeBytes
		b.StartTimer()

		start := time.Now()
		g, ctx := errgroup.WithContext(context.Background())
		for w := range benchWorkers {
			g.Go(func() error {
				for i := w; i < len(offsets); i += benchWorkers {
					off := offsets[i]
					length := min(blockSize, dataSize-off)
					if _, err := s.read(ctx, off, length); err != nil {
						return err
					}
				}

				return nil
			})
		}
		if err := g.Wait(); err != nil {
			b.Fatal(err)
		}
		totalElapsed += time.Since(start)

		b.StopTimer()
		b.ReportMetric(float64(s.fetchCount()), "fetches/op")
		s.close()
		b.StartTimer()
	}

	uMB := float64(dataSize) / (1024 * 1024)
	cMB := float64(storeBytes) / (1024 * 1024)
	b.ReportMetric(uMB, "U-MB/op")
	b.ReportMetric(cMB, "C-MB/op")
	if totalElapsed > 0 {
		b.ReportMetric(uMB/(totalElapsed.Seconds()/float64(b.N)), "U-MB/s")
	}
}

// runCacheHit warms the cache once, then measures b.N reads from cache.
func runCacheHit(b *testing.B, dataSize, blockSize int64, read benchReadF) {
	b.Helper()

	ctx := context.Background()
	for off := int64(0); off < dataSize; off += blockSize {
		_, err := read(ctx, off, min(blockSize, dataSize-off))
		require.NoError(b, err)
	}

	nOffsets := dataSize / blockSize
	b.ResetTimer()

	for i := range b.N {
		off := (int64(i) % nOffsets) * blockSize
		if _, err := read(ctx, off, blockSize); err != nil {
			b.Fatal(err)
		}
	}
}

// --- BenchmarkCacheHit ------------------------------------------------------

func BenchmarkCacheHit(b *testing.B) {
	data := generateSemiRandomData(benchDataSize)
	dataSize := int64(len(data))

	cases := []struct {
		name string
		read func(b *testing.B, blockSize int64) (benchReadF, func())
	}{
		{
			name: "Legacy",
			read: func(b *testing.B, blockSize int64) (benchReadF, func()) {
				b.Helper()
				c, err := newFullFetchChunker(dataSize, blockSize, &slowFrameGetter{data: data}, b.TempDir()+"/cache", newTestMetrics(b))
				require.NoError(b, err)

				return func(ctx context.Context, off, length int64) ([]byte, error) { return c.Slice(ctx, off, length) }, func() { c.Close() }
			},
		},
		{
			name: "Uncompressed",
			read: func(b *testing.B, blockSize int64) (benchReadF, func()) {
				b.Helper()
				c, err := NewChunker(&slowFrameGetter{data: data}, dataSize, blockSize, b.TempDir()+"/cache", newTestMetrics(b))
				require.NoError(b, err)

				return func(ctx context.Context, off, length int64) ([]byte, error) { return c.GetBlock(ctx, off, length, nil) }, func() { c.Close() }
			},
		},
	}

	for _, blockSize := range benchBlockSizes {
		b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
			for _, tc := range cases {
				b.Run(tc.name, func(b *testing.B) {
					read, cleanup := tc.read(b, blockSize)
					defer cleanup()
					runCacheHit(b, dataSize, blockSize, read)
				})
			}
		})
	}
}

// --- BenchmarkColdConcurrent ------------------------------------------------

func BenchmarkColdConcurrent(b *testing.B) {
	data := generateSemiRandomData(benchDataSize)
	dataSize := int64(len(data))

	// Precompute compressed data + frame tables for each codec config.
	type compressedBundle struct {
		ft             *storage.FrameTable
		compressedData []byte
	}
	bundles := make([]compressedBundle, len(benchCodecs))

	for ci, codec := range benchCodecs {
		up := &storage.MemPartUploader{}
		ft, _, err := storage.CompressStream(context.Background(), bytes.NewReader(data), &storage.FramedUploadOptions{
			CompressionType:    codec.compressionType,
			Level:              codec.level,
			EncoderConcurrency: 1,
			EncodeWorkers:      1,
			FrameSize:          codec.frameSize,
			TargetPartSize:     50 * 1024 * 1024,
		}, up)
		require.NoError(b, err)
		bundles[ci] = compressedBundle{ft, up.Assemble()}
	}

	for _, profile := range profiles {
		b.Run(profile.name, func(b *testing.B) {
			// Uncompressed paths: Legacy and new Chunker.
			b.Run("no-frame", func(b *testing.B) {
				for _, blockSize := range benchBlockSizes {
					b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
						b.Run("Legacy", func(b *testing.B) {
							runCold(b, dataSize, blockSize, profile, newColdSetup(data, dataSize, nil, nil, true))
						})
						b.Run("Uncompressed", func(b *testing.B) {
							runCold(b, dataSize, blockSize, profile, newColdSetup(data, dataSize, nil, nil, false))
						})
					})
				}
			})

			// Compressed paths: all codec options.
			for ci, codec := range benchCodecs {
				entry := bundles[ci]
				b.Run(codec.name, func(b *testing.B) {
					for _, blockSize := range benchBlockSizes {
						b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
							runCold(b, dataSize, blockSize, profile, newColdSetup(data, dataSize, entry.ft, entry.compressedData, false))
						})
					}
				})
			}
		})
	}
}

package block

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// ---------------------------------------------------------------------------
// Benchmark constants & dimensions
// ---------------------------------------------------------------------------

const benchDataSize = 100 * 1024 * 1024 // 100 MB

var benchFrameSizes = []int{
	1 * 1024 * 1024, // 1 MB
	2 * 1024 * 1024, // 2 MB
	4 * 1024 * 1024, // 4 MB (= MemoryChunkSize)
}

var benchBlockSizes = []int64{
	4 * 1024,        // 4 KB — typical VM page fault
	2 * 1024 * 1024, // 2 MB — large sequential read
}

// ---------------------------------------------------------------------------
// Backend profiles (simulated latency/bandwidth)
// ---------------------------------------------------------------------------

type backendProfile struct {
	name      string
	ttfb      time.Duration
	bandwidth int64 // bytes/sec
}

var profiles = []backendProfile{
	{name: "GCS", ttfb: 50 * time.Millisecond, bandwidth: 100 * 1024 * 1024},
	{name: "NFS", ttfb: 1 * time.Millisecond, bandwidth: 500 * 1024 * 1024},
}

// ---------------------------------------------------------------------------
// Codec configurations
// ---------------------------------------------------------------------------

type codecConfig struct {
	name            string
	compressionType storage.CompressionType
	level           int
}

var benchCodecs = []codecConfig{
	{name: "LZ4", compressionType: storage.CompressionLZ4, level: 0},
	{name: "Zstd1", compressionType: storage.CompressionZstd, level: 1},
	{name: "Zstd3", compressionType: storage.CompressionZstd, level: 3},
}

// ---------------------------------------------------------------------------
// Generic read function + setup types
// ---------------------------------------------------------------------------

type benchReadFunc func(ctx context.Context, off, length int64) ([]byte, error)

type coldSetup struct {
	read       benchReadFunc
	close      func()
	fetchCount func() int64
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

const benchWorkers = 4

func newBenchFlags(tb testing.TB) *MockFlagsClient {
	m := NewMockFlagsClient(tb)
	m.EXPECT().JSONFlag(mock.Anything, mock.Anything).Return(
		ldvalue.FromJSONMarshal(map[string]any{"minReadBatchSizeKB": 256}),
	).Maybe()

	return m
}

func generateSemiRandomData(size int) []byte {
	data := make([]byte, size)
	rng := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // deterministic for benchmarks

	// Random byte value repeated 1–16 times. Resembles real VM memory:
	// mostly random with occasional short runs (zero-filled structs, padding).
	// Kept short enough that compression stays under ~4x so frame count
	// scales with TargetFrameSize without hitting DefaultMaxFrameUncompressedSize.
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

func newBenchChunker(tb testing.TB, assets AssetInfo, blockSize int64) *Chunker {
	tb.Helper()

	c, err := NewChunker(assets, blockSize, tb.TempDir()+"/cache", newTestMetrics(tb), newBenchFlags(tb))
	require.NoError(tb, err)

	return c
}

func newFullFetchBench(tb testing.TB, upstream storage.FramedFile, size, blockSize int64) *fullFetchChunker {
	tb.Helper()

	c, err := newFullFetchChunker(size, blockSize, upstream, tb.TempDir()+"/cache", newTestMetrics(tb))
	require.NoError(tb, err)

	return c
}

func shuffledOffsets(dataSize, blockSize int64) []int64 {
	n := (dataSize + blockSize - 1) / blockSize
	offsets := make([]int64, n)
	for i := range offsets {
		offsets[i] = int64(i) * blockSize
	}
	rng := rand.New(rand.NewPCG(42, 99)) //nolint:gosec // deterministic for benchmarks
	rng.Shuffle(len(offsets), func(i, j int) {
		offsets[i], offsets[j] = offsets[j], offsets[i]
	})

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

func setCompressedAsset(a *AssetInfo, ct storage.CompressionType, file storage.FramedFile) {
	switch ct {
	case storage.CompressionLZ4:
		a.HasLZ4 = true
		a.LZ4 = file
	case storage.CompressionZstd:
		a.HasZstd = true
		a.Zstd = file
	}
}

// ---------------------------------------------------------------------------
// Leaf runners
// ---------------------------------------------------------------------------

// runColdLeaf runs a single cold-concurrent benchmark leaf (one profile, one
// blockSize, one mode). Each b.N iteration creates a fresh cold cache.
func runColdLeaf(b *testing.B, data []byte, blockSize int64, profile backendProfile, newIter func(tb testing.TB, slow *slowFrameGetter, blockSize int64) coldSetup) {
	b.Helper()

	dataSize := int64(len(data))
	offsets := shuffledOffsets(dataSize, blockSize)
	b.SetBytes(benchDataSize)
	b.ResetTimer()

	for range b.N {
		b.StopTimer()
		slow := &slowFrameGetter{data: data, ttfb: profile.ttfb, bandwidth: profile.bandwidth}
		s := newIter(b, slow, blockSize)
		b.StartTimer()

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

		b.StopTimer()
		b.ReportMetric(float64(s.fetchCount()), "fetches/op")
		s.close()
		b.StartTimer()
	}
}

// runCacheHitLeaf runs a single cache-hit benchmark leaf (one blockSize, one
// mode). Creates one chunker, warms the cache, then measures b.N reads.
func runCacheHitLeaf(b *testing.B, dataSize, blockSize int64, read benchReadFunc) {
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

// ---------------------------------------------------------------------------
// BenchmarkCacheHit
//
// block=4KB/
//
//	Legacy
//	Uncompressed
//
// block=2MB/
//
//	Legacy
//	Uncompressed
//
// ---------------------------------------------------------------------------
func BenchmarkCacheHit(b *testing.B) {
	data := generateSemiRandomData(benchDataSize)
	dataSize := int64(len(data))

	for _, blockSize := range benchBlockSizes {
		b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
			b.Run("Legacy", func(b *testing.B) {
				getter := &slowFrameGetter{data: data}
				c := newFullFetchBench(b, getter, dataSize, blockSize)
				defer c.Close()

				runCacheHitLeaf(b, dataSize, blockSize, func(ctx context.Context, off, length int64) ([]byte, error) {
					return c.Slice(ctx, off, length)
				})
			})

			b.Run("Uncompressed", func(b *testing.B) {
				getter := &slowFrameGetter{data: data}
				assets := AssetInfo{
					BasePath:        "bench",
					Size:            dataSize,
					HasUncompressed: true,
					Uncompressed:    getter,
				}
				c := newBenchChunker(b, assets, blockSize)
				defer c.Close()

				runCacheHitLeaf(b, dataSize, blockSize, func(ctx context.Context, off, length int64) ([]byte, error) {
					return c.GetBlock(ctx, off, length, nil)
				})
			})
		})
	}
}

// ---------------------------------------------------------------------------
// BenchmarkColdConcurrent
//
// GCS/
//
//	no-frame/
//	  block=4KB/
//	    Legacy
//	    Uncompressed
//	frame=1MB/
//	  block=4KB/
//	    LZ4
//	    Zstd1
//	    Zstd3
//
// NFS/
//
//	...
//
// ---------------------------------------------------------------------------
func BenchmarkColdConcurrent(b *testing.B) {
	data := generateSemiRandomData(benchDataSize)
	dataSize := int64(len(data))

	// Precompute frame tables so CompressBytes runs once per combo, not per profile.
	type ftEntry struct {
		ft *storage.FrameTable
	}
	type ftKey struct {
		frameSize int
		codecIdx  int
	}

	frameTables := make(map[ftKey]ftEntry)

	for _, frameSize := range benchFrameSizes {
		for ci, codec := range benchCodecs {
			_, ft, err := storage.CompressBytes(context.Background(), data, &storage.FramedUploadOptions{
				CompressionType:          codec.compressionType,
				Level:                    codec.level,
				CompressionConcurrency:   1,
				TargetFrameSize:          frameSize,
				MaxUncompressedFrameSize: storage.DefaultMaxFrameUncompressedSize,
				TargetPartSize:           50 * 1024 * 1024,
			})
			require.NoError(b, err)

			frameTables[ftKey{frameSize, ci}] = ftEntry{ft}
		}
	}

	legacyFactory := func(tb testing.TB, slow *slowFrameGetter, blockSize int64) coldSetup {
		c := newFullFetchBench(tb, slow, dataSize, blockSize)

		return coldSetup{
			read:       func(ctx context.Context, off, length int64) ([]byte, error) { return c.Slice(ctx, off, length) },
			close:      func() { c.Close() },
			fetchCount: func() int64 { return slow.fetchCount.Load() },
		}
	}

	uncompressedFactory := func(tb testing.TB, slow *slowFrameGetter, blockSize int64) coldSetup {
		assets := AssetInfo{
			BasePath:        "bench",
			Size:            dataSize,
			HasUncompressed: true,
			Uncompressed:    slow,
		}
		c := newBenchChunker(tb, assets, blockSize)

		return coldSetup{
			read:       func(ctx context.Context, off, length int64) ([]byte, error) { return c.GetBlock(ctx, off, length, nil) },
			close:      func() { c.Close() },
			fetchCount: func() int64 { return slow.fetchCount.Load() },
		}
	}

	for _, profile := range profiles {
		b.Run(profile.name, func(b *testing.B) {
			// Uncompressed: no-frame → block → {Legacy, Uncompressed}
			b.Run("no-frame", func(b *testing.B) {
				for _, blockSize := range benchBlockSizes {
					b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
						b.Run("Legacy", func(b *testing.B) {
							runColdLeaf(b, data, blockSize, profile, legacyFactory)
						})
						b.Run("Uncompressed", func(b *testing.B) {
							runColdLeaf(b, data, blockSize, profile, uncompressedFactory)
						})
					})
				}
			})

			// Compressed: frame → block → codec
			for _, frameSize := range benchFrameSizes {
				b.Run(fmt.Sprintf("frame=%s", fmtSize(int64(frameSize))), func(b *testing.B) {
					for _, blockSize := range benchBlockSizes {
						b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
							for ci, codec := range benchCodecs {
								ft := frameTables[ftKey{frameSize, ci}].ft

								b.Run(codec.name, func(b *testing.B) {
									runColdLeaf(b, data, blockSize, profile, func(tb testing.TB, slow *slowFrameGetter, blockSize int64) coldSetup {
										assets := AssetInfo{
											BasePath: "bench",
											Size:     dataSize,
										}
										setCompressedAsset(&assets, codec.compressionType, slow)
										c := newBenchChunker(tb, assets, blockSize)

										return coldSetup{
											read:       func(ctx context.Context, off, length int64) ([]byte, error) { return c.GetBlock(ctx, off, length, ft) },
											close:      func() { c.Close() },
											fetchCount: func() int64 { return slow.fetchCount.Load() },
										}
									})
								})
							}
						})
					}
				})
			}
		})
	}
}

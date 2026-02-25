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

// Frame sizes for compressed path (uncompressed frame granularity).
// Uncompressed path always uses MemoryChunkSize (4 MB) regardless.
var benchFrameSizes = []int{
	1 * 1024 * 1024, // 1 MB → 100 frames over 100 MB
	2 * 1024 * 1024, // 2 MB → 50 frames
	4 * 1024 * 1024, // 4 MB → 25 frames (= MemoryChunkSize)
}

// Block sizes: the `length` parameter to GetBlock().
// 4 KB = typical VM page fault, 2 MB = large sequential read / prefetch.
var benchBlockSizes = []int64{
	4 * 1024,        // 4 KB
	2 * 1024 * 1024, // 2 MB
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
// Benchmark helpers
// ---------------------------------------------------------------------------

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

	i := 0
	for i < size {
		runLen := rng.IntN(4096) + 1
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

func warmCache(b *testing.B, chunker *Chunker, ft *storage.FrameTable, blockSize int64) {
	b.Helper()

	ctx := context.Background()
	for off := int64(0); off < chunker.assets.Size; off += blockSize {
		length := min(blockSize, chunker.assets.Size-off)
		_, err := chunker.GetBlock(ctx, off, length, ft)
		require.NoError(b, err)
	}
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

// setCompressedAsset sets the appropriate Has*/field on AssetInfo for the given compression type.
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
// BenchmarkGetBlock
// ---------------------------------------------------------------------------

func BenchmarkGetBlock(b *testing.B) {
	data := generateSemiRandomData(benchDataSize)

	b.Run("CacheHit", func(b *testing.B) {
		benchCacheHit(b, data)
	})
	b.Run("ColdConcurrent", func(b *testing.B) {
		benchColdConcurrent(b, data)
	})
}

// ---------------------------------------------------------------------------
// CacheHit — measures mmap fast-path (bitmap check + slice return)
// ---------------------------------------------------------------------------

func benchCacheHit(b *testing.B, data []byte) {
	b.Helper()

	b.Run("Uncompressed", func(b *testing.B) {
		for _, blockSize := range benchBlockSizes {
			b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
				assets := AssetInfo{
					BasePath:        "bench",
					Size:            int64(len(data)),
					HasUncompressed: true,
					Uncompressed:    &slowFrameGetter{data: data},
				}
				c := newBenchChunker(b, assets, blockSize)
				defer c.Close()

				warmCache(b, c, nil, blockSize)

				nOffsets := int64(len(data)) / blockSize
				b.ResetTimer()
				for i := range b.N {
					off := (int64(i) % nOffsets) * blockSize
					_, err := c.GetBlock(context.Background(), off, blockSize, nil)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})

	for _, codec := range benchCodecs {
		b.Run(codec.name, func(b *testing.B) {
			for _, frameSize := range benchFrameSizes {
				b.Run(fmt.Sprintf("frame=%s", fmtSize(int64(frameSize))), func(b *testing.B) {
					_, ft, err := storage.CompressBytes(context.Background(), data, &storage.FramedUploadOptions{
						CompressionType:          codec.compressionType,
						Level:                    codec.level,
						CompressionConcurrency:   1,
						TargetFrameSize:          frameSize,
						MaxUncompressedFrameSize: storage.DefaultMaxFrameUncompressedSize,
						TargetPartSize:           50 * 1024 * 1024,
					})
					require.NoError(b, err)

					for _, blockSize := range benchBlockSizes {
						b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
							getter := &slowFrameGetter{data: data}
							assets := AssetInfo{
								BasePath: "bench",
								Size:     int64(len(data)),
							}
							setCompressedAsset(&assets, codec.compressionType, getter)
							c := newBenchChunker(b, assets, blockSize)
							defer c.Close()

							warmCache(b, c, ft, blockSize)

							nOffsets := int64(len(data)) / blockSize
							b.ResetTimer()
							for i := range b.N {
								off := (int64(i) % nOffsets) * blockSize
								_, err := c.GetBlock(context.Background(), off, blockSize, ft)
								if err != nil {
									b.Fatal(err)
								}
							}
						})
					}
				})
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ColdConcurrent — concurrent cold-start fetch with simulated latency
//
// Multiple workers read the entire image from cold cache, each offset
// touched exactly once (round-robin split of shuffled offsets).
// Tests session dedup and fetchSession fan-out under concurrency.
// ---------------------------------------------------------------------------

const benchWorkers = 4

func benchColdConcurrent(b *testing.B, data []byte) {
	b.Helper()

	b.Run("Uncompressed", func(b *testing.B) {
		for _, profile := range profiles {
			b.Run(profile.name, func(b *testing.B) {
				for _, blockSize := range benchBlockSizes {
					b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
						offsets := shuffledOffsets(int64(len(data)), blockSize)
						b.SetBytes(benchDataSize)
						b.ResetTimer()

						for range b.N {
							b.StopTimer()
							slow := &slowFrameGetter{data: data, ttfb: profile.ttfb, bandwidth: profile.bandwidth}
							assets := AssetInfo{
								BasePath:        "bench",
								Size:            int64(len(data)),
								HasUncompressed: true,
								Uncompressed:    slow,
							}
							c := newBenchChunker(b, assets, blockSize)
							b.StartTimer()

							g, ctx := errgroup.WithContext(context.Background())
							for w := range benchWorkers {
								g.Go(func() error {
									for i := w; i < len(offsets); i += benchWorkers {
										off := offsets[i]
										length := min(blockSize, int64(len(data))-off)
										if _, err := c.GetBlock(ctx, off, length, nil); err != nil {
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
							b.ReportMetric(float64(slow.fetchCount.Load()), "fetches/op")
							c.Close()
							b.StartTimer()
						}
					})
				}
			})
		}
	})

	for _, codec := range benchCodecs {
		b.Run(codec.name, func(b *testing.B) {
			for _, frameSize := range benchFrameSizes {
				b.Run(fmt.Sprintf("frame=%s", fmtSize(int64(frameSize))), func(b *testing.B) {
					_, ft, err := storage.CompressBytes(context.Background(), data, &storage.FramedUploadOptions{
						CompressionType:          codec.compressionType,
						Level:                    codec.level,
						CompressionConcurrency:   1,
						TargetFrameSize:          frameSize,
						MaxUncompressedFrameSize: storage.DefaultMaxFrameUncompressedSize,
						TargetPartSize:           50 * 1024 * 1024,
					})
					require.NoError(b, err)

					for _, profile := range profiles {
						b.Run(profile.name, func(b *testing.B) {
							for _, blockSize := range benchBlockSizes {
								b.Run(fmt.Sprintf("block=%s", fmtSize(blockSize)), func(b *testing.B) {
									offsets := shuffledOffsets(int64(len(data)), blockSize)
									b.SetBytes(benchDataSize)
									b.ResetTimer()

									for range b.N {
										b.StopTimer()
										slow := &slowFrameGetter{data: data, ttfb: profile.ttfb, bandwidth: profile.bandwidth}
										assets := AssetInfo{
											BasePath: "bench",
											Size:     int64(len(data)),
										}
										setCompressedAsset(&assets, codec.compressionType, slow)
										c := newBenchChunker(b, assets, blockSize)
										b.StartTimer()

										g, ctx := errgroup.WithContext(context.Background())
										for w := range benchWorkers {
											g.Go(func() error {
												for i := w; i < len(offsets); i += benchWorkers {
													off := offsets[i]
													length := min(blockSize, int64(len(data))-off)
													if _, err := c.GetBlock(ctx, off, length, ft); err != nil {
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
										b.ReportMetric(float64(slow.fetchCount.Load()), "fetches/op")
										c.Close()
										b.StartTimer()
									}
								})
							}
						})
					}
				})
			}
		})
	}
}

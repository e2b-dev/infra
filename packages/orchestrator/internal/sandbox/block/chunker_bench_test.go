package block

import (
	"context"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// Benchmark parameters
const (
	benchFrameSize     = 4 * 1024 * 1024    // 4MB per frame
	benchReadSize      = 4096               // 4KB reads (typical page fault)
	benchTotalDataSize = 1024 * 1024 * 1024 // 1GB total
	benchNumFrames     = benchTotalDataSize / benchFrameSize

	// LRU holds 30% of uncompressed data
	benchLRUPercent = 30
	benchLRUBytes   = benchTotalDataSize * benchLRUPercent / 100
)

// GCS/S3 simulation parameters
const (
	gcsLatency   = 20 * time.Millisecond
	gcsBandwidth = 200.0 // MB/s
)

func generateSemiRandomData(size int, seed int64) []byte {
	r := rand.New(rand.NewSource(seed))
	data := make([]byte, size)
	pos := 0
	for pos < size {
		val := byte(r.Intn(256))
		repeatLen := r.Intn(32) + 1
		if pos+repeatLen > size {
			repeatLen = size - pos
		}
		for i := 0; i < repeatLen; i++ {
			data[pos+i] = val
		}
		pos += repeatLen
	}
	return data
}

func setupCompressedStorage(b *testing.B, baseDir string) (*storage.Storage, string, *storage.FrameTable) {
	b.Helper()
	st := storage.NewLocalStorage(baseDir)
	origData := generateSemiRandomData(benchTotalDataSize, 42)
	inputFile := filepath.Join(baseDir, "input.dat")
	require.NoError(b, os.WriteFile(inputFile, origData, 0o644))
	objectPath := "compressed.zst"
	frameTable, err := st.StoreFile(context.Background(), inputFile, objectPath, storage.DefaultCompressionOptions)
	require.NoError(b, err)
	os.Remove(inputFile)
	compressedSize := frameTable.TotalCompressedSize()
	ratio := float64(benchTotalDataSize) / float64(compressedSize)
	b.Logf("Compressed: %dMB -> %dMB (%.2fx), %d frames", benchTotalDataSize>>20, compressedSize>>20, ratio, len(frameTable.Frames))
	return st, objectPath, frameTable
}

func setupUncompressedStorage(b *testing.B, baseDir string) (*storage.Storage, string) {
	b.Helper()
	st := storage.NewLocalStorage(baseDir)
	origData := generateSemiRandomData(benchTotalDataSize, 42)
	objectPath := "uncompressed.raw"
	require.NoError(b, os.WriteFile(filepath.Join(baseDir, objectPath), origData, 0o644))
	b.Logf("Uncompressed: %dMB", benchTotalDataSize>>20)
	return st, objectPath
}

func lruFrameCount(frameTable *storage.FrameTable, targetBytes int64) int {
	bytesPerFrame := int64(benchTotalDataSize / len(frameTable.Frames))
	frames := int(targetBytes / bytesPerFrame)
	if frames < 1 {
		frames = 1
	}
	return frames
}

// =============================================================================
// LocalHit: Data already in local cache (mmap page cache or LRU memory)
// =============================================================================

func BenchmarkSlice_LocalHit(b *testing.B) {
	compDir, uncompDir, cacheDir := b.TempDir(), b.TempDir(), b.TempDir()
	cSt, cPath, cFT := setupCompressedStorage(b, compDir)
	uSt, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	// Access within frame 0 only (guaranteed hit after warmup)
	offsets := make([]int64, 10000)
	for i := range offsets {
		offsets[i] = int64((i * 7919) % (benchFrameSize - benchReadSize))
	}

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, uSt, uPath, nil, filepath.Join(cacheDir, "u"), benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize)
		}
	})

	b.Run("Compressed_LRU", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, cFT, lruSize, benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize)
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, cSt, cPath, cFT, filepath.Join(cacheDir, "c"), benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize)
		}
	})
}

// =============================================================================
// LRUMiss: LRU eviction forces re-decompression (CompressLRU only)
// For MMap chunkers, this is same as LocalHit (disk cache is permanent)
// =============================================================================

func BenchmarkSlice_LRUMiss(b *testing.B) {
	compDir := b.TempDir()
	cSt, cPath, cFT := setupCompressedStorage(b, compDir)
	ctx := context.Background()

	numFrames := len(cFT.Frames)
	bytesPerFrame := benchTotalDataSize / numFrames

	b.Run("Compressed_LRU_Eviction", func(b *testing.B) {
		// LRU=1 forces eviction on every frame change
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, cFT, 1, benchMetrics(b))
		defer chunker.Close()

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// Alternate between first and last frame to force eviction
			frameIdx := (i % 2) * (numFrames - 1)
			offset := int64(frameIdx * bytesPerFrame)
			chunker.Slice(ctx, offset, benchReadSize)
		}
	})
}

// =============================================================================
// ColdFetch: Fetch 32MB of uncompressed data from backend (cold cache)
// This measures: "To access 32MB of logical data, how much network I/O?"
// - Uncompressed: must fetch 32MB (8 x 4MB blocks)
// - Compressed: fetches ~4MB (1 frame covering 33MB)
// =============================================================================

func BenchmarkSlice_ColdFetch_32MB(b *testing.B) {
	compDir, uncompDir := b.TempDir(), b.TempDir()
	_, cPath, cFT := setupCompressedStorage(b, compDir)
	_, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	const targetUncompressedBytes = 32 * 1024 * 1024 // 32MB of logical data
	numCompFrames := len(cFT.Frames)
	bytesPerCompFrame := benchTotalDataSize / numCompFrames

	b.Logf("Target: access %dMB of uncompressed data", targetUncompressedBytes>>20)
	b.Logf("Uncompressed: will fetch %dMB in %d x 4MB blocks",
		targetUncompressedBytes>>20, targetUncompressedBytes/storage.MemoryChunkSize)
	b.Logf("Compressed: 1 frame covers %dMB uncompressed, ~%dMB compressed",
		bytesPerCompFrame>>20, (bytesPerCompFrame/8)>>20)

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(uncompDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, uPath, nil, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (8 x 4MB blocks)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize)
			}

			b.StopTimer()
			bytes, calls := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "network-MB")
				b.ReportMetric(float64(calls), "requests")
			}
			chunker.Close()
		}
	})

	b.Run("Compressed_LRU", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressLRUChunker(benchTotalDataSize, slowSt, cPath, cFT, numCompFrames, benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (fits in 1 compressed frame)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize)
			}

			b.StopTimer()
			bytes, calls := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "network-MB")
				b.ReportMetric(float64(calls), "requests")
			}
			chunker.Close()
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, cPath, cFT, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (fits in 1 compressed frame)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize)
			}

			b.StopTimer()
			bytes, calls := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "network-MB")
				b.ReportMetric(float64(calls), "requests")
			}
			chunker.Close()
		}
	})
}

// =============================================================================
// MixedWorkload: 20% hot region, 80% random (realistic access pattern)
// =============================================================================

func BenchmarkSlice_MixedWorkload(b *testing.B) {
	compDir, uncompDir, cacheDir := b.TempDir(), b.TempDir(), b.TempDir()
	cSt, cPath, cFT := setupCompressedStorage(b, compDir)
	uSt, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	// 20% hot (first 2 frames), 80% random
	const patternSize = 10000
	r := rand.New(rand.NewSource(123))
	hotRegion := 2 * benchFrameSize
	offsets := make([]int64, patternSize)
	for i := range offsets {
		if r.Intn(100) < 20 {
			offsets[i] = int64(r.Intn(hotRegion - benchReadSize))
		} else {
			offsets[i] = int64(r.Intn(benchTotalDataSize - benchReadSize))
		}
	}

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, uSt, uPath, nil, filepath.Join(cacheDir, "u"), benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize)
		}
	})

	b.Run("Compressed_LRU_30pct", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, cFT, lruSize, benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize)
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, cSt, cPath, cFT, filepath.Join(cacheDir, "c"), benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize)
		}
	})
}

// =============================================================================
// FullFetch: Time to fetch entire dataset from backend (cold start)
// =============================================================================

func BenchmarkSlice_FullFetch(b *testing.B) {
	compDir, uncompDir := b.TempDir(), b.TempDir()
	_, cPath, cFT := setupCompressedStorage(b, compDir)
	_, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	numCompFrames := len(cFT.Frames)
	bytesPerCompFrame := benchTotalDataSize / numCompFrames

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(uncompDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, uPath, nil, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			for f := 0; f < benchNumFrames; f++ {
				chunker.Slice(ctx, int64(f*benchFrameSize), benchReadSize)
			}

			b.StopTimer()
			bytes, _ := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "total-fetched-MB")
			}
			chunker.Close()
		}
	})

	b.Run("Compressed_LRU", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressLRUChunker(benchTotalDataSize, slowSt, cPath, cFT, numCompFrames, benchMetrics(b))
			b.StartTimer()

			for f := 0; f < numCompFrames; f++ {
				chunker.Slice(ctx, int64(f*bytesPerCompFrame), benchReadSize)
			}

			b.StopTimer()
			bytes, _ := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "total-fetched-MB")
			}
			chunker.Close()
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, cPath, cFT, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			for f := 0; f < numCompFrames; f++ {
				chunker.Slice(ctx, int64(f*bytesPerCompFrame), benchReadSize)
			}

			b.StopTimer()
			bytes, _ := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "total-fetched-MB")
			}
			chunker.Close()
		}
	})
}

// =============================================================================
// Helpers
// =============================================================================

func benchMetrics(b *testing.B) metrics.Metrics {
	b.Helper()
	m, _ := metrics.NewMetrics(noop.NewMeterProvider())
	return m
}

type slowFileSystem struct {
	*storage.FileSystem
	baseLatency time.Duration
	bandwidth   float64
	readBytes   atomic.Int64
	readCalls   atomic.Int64
}

func (s *slowFileSystem) simulateDelay(bytes int) {
	delay := s.baseLatency
	if s.bandwidth > 0 && bytes > 0 {
		delay += time.Duration(float64(bytes) / s.bandwidth * float64(time.Second))
	}
	time.Sleep(delay)
}

func (s *slowFileSystem) RangeGet(ctx context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	s.simulateDelay(length)
	s.readBytes.Add(int64(length))
	s.readCalls.Add(1)
	return s.FileSystem.RangeGet(ctx, path, offset, length)
}

func (s *slowFileSystem) StartDownload(ctx context.Context, path string) (io.ReadCloser, error) {
	s.simulateDelay(0)
	s.readCalls.Add(1)
	return s.FileSystem.StartDownload(ctx, path)
}

func (s *slowFileSystem) stats() (int64, int64) {
	return s.readBytes.Load(), s.readCalls.Load()
}

func newSlowStorage(basePath string, baseLatency time.Duration, bandwidthMBps float64) (*storage.Storage, *slowFileSystem) {
	fs := storage.NewFS(basePath).Basic.(*storage.FileSystem)
	slow := &slowFileSystem{
		FileSystem:  fs,
		baseLatency: baseLatency,
		bandwidth:   bandwidthMBps * 1024 * 1024,
	}
	return &storage.Storage{
		Backend: &storage.Backend{
			Basic:                    slow,
			Manager:                  fs,
			MultipartUploaderFactory: fs,
			RangeGetter:              slow,
		},
	}, slow
}

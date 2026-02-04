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

// GCS/S3 simulation parameters (based on real measurements from integration tests)
const (
	gcsLatency   = 50 * time.Millisecond // measured: 37-134ms, typical 50-90ms
	gcsBandwidth = 100.0                 // MB/s - measured: 30-115MB/s, typical 45-70MB/s
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
		for i := range repeatLen {
			data[pos+i] = val
		}
		pos += repeatLen
	}

	return data
}

func setupCompressedStorage(b *testing.B, baseDir string) (*storage.Storage, string, *storage.FrameTable, int64) {
	b.Helper()
	st := storage.NewFileSystemStorage(baseDir)
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

	return st, objectPath, frameTable, compressedSize
}

func setupUncompressedStorage(b *testing.B, baseDir string) (*storage.Storage, string) {
	b.Helper()
	st := storage.NewFileSystemStorage(baseDir)
	origData := generateSemiRandomData(benchTotalDataSize, 42)
	objectPath := "uncompressed.raw"
	require.NoError(b, os.WriteFile(filepath.Join(baseDir, objectPath), origData, 0o644))
	b.Logf("Uncompressed: %dMB", benchTotalDataSize>>20)

	return st, objectPath
}

func lruFrameCount(frameTable *storage.FrameTable, targetBytes int64) int {
	bytesPerFrame := int64(benchTotalDataSize / len(frameTable.Frames))
	frames := max(int(targetBytes/bytesPerFrame), 1)

	return frames
}

// =============================================================================
// LocalHit: Data already in local cache (mmap page cache or LRU memory)
// =============================================================================

func BenchmarkSlice_LocalHit(b *testing.B) {
	compDir, uncompDir, cacheDir := b.TempDir(), b.TempDir(), b.TempDir()
	cSt, cPath, cFT, cRawSize := setupCompressedStorage(b, compDir)
	uSt, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	// Access within frame 0 only (guaranteed hit after warmup)
	offsets := make([]int64, 10000)
	for i := range offsets {
		offsets[i] = int64((i * 7919) % (benchFrameSize - benchReadSize))
	}

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, uSt, uPath, filepath.Join(cacheDir, "u"), benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize, nil) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize, nil)
		}
	})

	b.Run("Compressed_LRU", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, lruSize, benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize, cFT) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize, cFT)
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, cRawSize, storage.MemoryChunkSize, cSt, cPath, filepath.Join(cacheDir, "c"), benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize, cFT) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize, cFT)
		}
	})

	b.Run("Compressed_MMapLRU", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressMMapLRUChunker(benchTotalDataSize, cRawSize, cSt, cPath, filepath.Join(cacheDir, "mlru"), lruSize, benchMetrics(b))
		defer chunker.Close()
		chunker.Slice(ctx, 0, benchReadSize, cFT) // warm

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%len(offsets)], benchReadSize, cFT)
		}
	})
}

// =============================================================================
// LRUMiss: LRU eviction forces re-decompression (CompressLRU only)
// For MMap chunkers, this is same as LocalHit (disk cache is permanent)
// =============================================================================

func BenchmarkSlice_LRUMiss(b *testing.B) {
	compDir := b.TempDir()
	cSt, cPath, cFT, cRawSize := setupCompressedStorage(b, compDir)
	ctx := context.Background()

	numFrames := len(cFT.Frames)
	bytesPerFrame := benchTotalDataSize / numFrames

	b.Run("Compressed_LRU_Eviction", func(b *testing.B) {
		// LRU=1 forces eviction on every frame change
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, 1, benchMetrics(b))
		defer chunker.Close()

		b.ResetTimer()
		for i := range b.N {
			// Alternate between first and last frame to force eviction
			frameIdx := (i % 2) * (numFrames - 1)
			offset := int64(frameIdx * bytesPerFrame)
			chunker.Slice(ctx, offset, benchReadSize, cFT)
		}
	})

	b.Run("Compressed_MMapLRU_Eviction", func(b *testing.B) {
		// LRU=1 forces eviction, but mmap keeps compressed frames locally
		cacheDir := b.TempDir()
		chunker, _ := NewCompressMMapLRUChunker(benchTotalDataSize, cRawSize, cSt, cPath, filepath.Join(cacheDir, "mlru"), 1, benchMetrics(b))
		defer chunker.Close()

		// Warm up: access both frames to populate mmap cache
		chunker.Slice(ctx, 0, benchReadSize, cFT)
		chunker.Slice(ctx, int64((numFrames-1)*bytesPerFrame), benchReadSize, cFT)

		b.ResetTimer()
		for i := range b.N {
			// Alternate between first and last frame
			// LRU evicts, but mmap cache serves compressed data locally
			frameIdx := (i % 2) * (numFrames - 1)
			offset := int64(frameIdx * bytesPerFrame)
			chunker.Slice(ctx, offset, benchReadSize, cFT)
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
	_, cPath, cFT, cRawSize := setupCompressedStorage(b, compDir)
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
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(uncompDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, uPath, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (8 x 4MB blocks)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize, nil)
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
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressLRUChunker(benchTotalDataSize, slowSt, cPath, numCompFrames, benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (fits in 1 compressed frame)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize, cFT)
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
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, cRawSize, storage.MemoryChunkSize, slowSt, cPath, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (fits in 1 compressed frame)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize, cFT)
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

	b.Run("Compressed_MMapLRU", func(b *testing.B) {
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressMMapLRUChunker(benchTotalDataSize, cRawSize, slowSt, cPath, filepath.Join(b.TempDir(), "mlru"), numCompFrames, benchMetrics(b))
			b.StartTimer()

			// Access 32MB of data (fits in 1 compressed frame)
			for off := int64(0); off < targetUncompressedBytes; off += storage.MemoryChunkSize {
				chunker.Slice(ctx, off, benchReadSize, cFT)
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
	cSt, cPath, cFT, cRawSize := setupCompressedStorage(b, compDir)
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
		chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, uSt, uPath, filepath.Join(cacheDir, "u"), benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize, nil)
		}
	})

	b.Run("Compressed_LRU_30pct", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressLRUChunker(benchTotalDataSize, cSt, cPath, lruSize, benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize, cFT)
		}
	})

	b.Run("Compressed_MMap", func(b *testing.B) {
		chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, cRawSize, storage.MemoryChunkSize, cSt, cPath, filepath.Join(cacheDir, "c"), benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize, cFT)
		}
	})

	b.Run("Compressed_MMapLRU_30pct", func(b *testing.B) {
		lruSize := lruFrameCount(cFT, benchLRUBytes)
		chunker, _ := NewCompressMMapLRUChunker(benchTotalDataSize, cRawSize, cSt, cPath, filepath.Join(cacheDir, "mlru"), lruSize, benchMetrics(b))
		defer chunker.Close()

		b.SetBytes(benchReadSize)
		b.ResetTimer()
		for i := range b.N {
			chunker.Slice(ctx, offsets[i%patternSize], benchReadSize, cFT)
		}
	})
}

// =============================================================================
// FullFetch: Time to fetch entire dataset from backend (cold start)
// =============================================================================

func BenchmarkSlice_FullFetch(b *testing.B) {
	compDir, uncompDir := b.TempDir(), b.TempDir()
	_, cPath, cFT, cRawSize := setupCompressedStorage(b, compDir)
	_, uPath := setupUncompressedStorage(b, uncompDir)
	ctx := context.Background()

	numCompFrames := len(cFT.Frames)
	bytesPerCompFrame := benchTotalDataSize / numCompFrames

	b.Run("Uncompressed_MMap", func(b *testing.B) {
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(uncompDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewUncompressedMMapChunker(benchTotalDataSize, storage.MemoryChunkSize, slowSt, uPath, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			for f := range benchNumFrames {
				chunker.Slice(ctx, int64(f*benchFrameSize), benchReadSize, nil)
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
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressLRUChunker(benchTotalDataSize, slowSt, cPath, numCompFrames, benchMetrics(b))
			b.StartTimer()

			for f := range numCompFrames {
				chunker.Slice(ctx, int64(f*bytesPerCompFrame), benchReadSize, cFT)
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
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewDecompressMMapChunker(benchTotalDataSize, cRawSize, storage.MemoryChunkSize, slowSt, cPath, filepath.Join(b.TempDir(), "c"), benchMetrics(b))
			b.StartTimer()

			for f := range numCompFrames {
				chunker.Slice(ctx, int64(f*bytesPerCompFrame), benchReadSize, cFT)
			}

			b.StopTimer()
			bytes, _ := slowFS.stats()
			if i == 0 {
				b.ReportMetric(float64(bytes>>20), "total-fetched-MB")
			}
			chunker.Close()
		}
	})

	b.Run("Compressed_MMapLRU", func(b *testing.B) {
		for i := range b.N {
			b.StopTimer()
			slowSt, slowFS := newSlowStorage(compDir, gcsLatency, gcsBandwidth)
			chunker, _ := NewCompressMMapLRUChunker(benchTotalDataSize, cRawSize, slowSt, cPath, filepath.Join(b.TempDir(), "mlru"), numCompFrames, benchMetrics(b))
			b.StartTimer()

			for f := range numCompFrames {
				chunker.Slice(ctx, int64(f*bytesPerCompFrame), benchReadSize, cFT)
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

// SlowFrameGetter wraps a FrameGetter and adds simulated network delays.
// Use this to test chunker behavior with slow storage.
type SlowFrameGetter struct {
	inner         storage.FrameGetter
	latency       time.Duration
	bandwidthMBps float64
	readBytes     atomic.Int64
	readCalls     atomic.Int64
}

// Slowdown wraps any FrameGetter with simulated network delays.
// Works with real storage, mocks, or any FrameGetter implementation.
//
// Parameters:
//   - fg: the underlying FrameGetter to wrap
//   - latency: base latency added to each request
//   - bandwidthMBps: bandwidth limit in MB/s (0 = unlimited)
func Slowdown(fg storage.FrameGetter, latency time.Duration, bandwidthMBps float64) *SlowFrameGetter {
	return &SlowFrameGetter{
		inner:         fg,
		latency:       latency,
		bandwidthMBps: bandwidthMBps,
	}
}

func (s *SlowFrameGetter) simulateDelay(bytes int) {
	delay := s.latency
	if s.bandwidthMBps > 0 && bytes > 0 {
		bandwidth := s.bandwidthMBps * 1024 * 1024
		delay += time.Duration(float64(bytes) / bandwidth * float64(time.Second))
	}
	time.Sleep(delay)
}

func (s *SlowFrameGetter) GetFrame(ctx context.Context, objectPath string, offsetU int64, frameTable *storage.FrameTable, decompress bool, buf []byte) (storage.Range, error) {
	s.simulateDelay(len(buf))
	s.readBytes.Add(int64(len(buf)))
	s.readCalls.Add(1)

	return s.inner.GetFrame(ctx, objectPath, offsetU, frameTable, decompress, buf)
}

// Stats returns total bytes read and number of calls.
func (s *SlowFrameGetter) Stats() (bytesRead, calls int64) {
	return s.readBytes.Load(), s.readCalls.Load()
}

// slowFileSystem wraps FileSystem for benchmarks that need full storage.Storage.
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

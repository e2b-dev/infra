package block

import (
	"fmt"
	"os"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// BenchmarkDirtyTracking benchmarks the dirty tracking operations
// which were changed from sync.Map to bitset.BitSet.
func BenchmarkDirtyTracking(b *testing.B) {
	const (
		blockSize = int64(header.PageSize) // 4KB blocks
		cacheSize = 1024 * 1024 * 1024     // 1GB cache = 262144 blocks
	)

	tmpFile := b.TempDir() + "/bench_cache"

	cache, err := NewCache(cacheSize, blockSize, tmpFile, false)
	if err != nil {
		b.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Simulate write data
	data := make([]byte, blockSize)

	b.Run("SetIsCached", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			// Simulate marking a block as cached
			off := int64(i%262144) * blockSize
			cache.setIsCached(off, blockSize)
		}
	})

	b.Run("IsCached_Hit", func(b *testing.B) {
		// Pre-populate some blocks as cached
		for i := range int64(1000) {
			cache.setIsCached(i*blockSize, blockSize)
		}

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			off := int64(i%1000) * blockSize
			cache.isCached(off, blockSize)
		}
	})

	b.Run("IsCached_Miss", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			// Check blocks that are definitely not cached (high offsets)
			off := int64(100000+i%100000) * blockSize
			cache.isCached(off, blockSize)
		}
	})

	b.Run("WriteAt_SingleBlock", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			off := int64(i%262144) * blockSize
			cache.WriteAt(data, off)
		}
	})

	b.Run("WriteAt_MultiBlock", func(b *testing.B) {
		// Write spanning 4 blocks
		multiBlockData := make([]byte, blockSize*4)

		b.ReportAllocs()
		b.ResetTimer()

		for i := range b.N {
			off := int64(i%65536) * blockSize * 4
			cache.WriteAt(multiBlockData, off)
		}
	})
}

// BenchmarkDirtySortedKeys benchmarks the dirtySortedKeys operation
// used during export.
func BenchmarkDirtySortedKeys(b *testing.B) {
	const (
		blockSize = int64(header.PageSize)
		cacheSize = 1024 * 1024 * 1024 // 1GB
	)

	tmpFile := b.TempDir() + "/bench_cache"

	cache, err := NewCache(cacheSize, blockSize, tmpFile, false)
	if err != nil {
		b.Fatalf("failed to create cache: %v", err)
	}
	defer cache.Close()

	// Mark 10% of blocks as dirty (26214 blocks)
	for i := range int64(26214) {
		cache.setIsCached(i*blockSize, blockSize)
	}

	b.Run("DirtySortedKeys_10pct", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()

		for range b.N {
			keys := cache.dirtySortedKeys()
			_ = keys
		}
	})
}

// BenchmarkCacheCreation benchmarks cache creation overhead.
func BenchmarkCacheCreation(b *testing.B) {
	const (
		blockSize = int64(header.PageSize)
	)

	sizes := []int64{
		1 * 1024 * 1024,         // 1MB
		100 * 1024 * 1024,       // 100MB
		1024 * 1024 * 1024,      // 1GB
		10 * 1024 * 1024 * 1024, // 10GB
	}

	for _, size := range sizes {
		name := formatSize(size)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()

			for range b.N {
				tmpFile := b.TempDir() + "/bench_cache"
				cache, err := NewCache(size, blockSize, tmpFile, false)
				if err != nil {
					b.Fatalf("failed to create cache: %v", err)
				}
				cache.Close()
				os.RemoveAll(tmpFile)
			}
		})
	}
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%dGB", bytes/GB)
	case bytes >= MB:
		return fmt.Sprintf("%dMB", bytes/MB)
	default:
		return fmt.Sprintf("%dKB", bytes/KB)
	}
}

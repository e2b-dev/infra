package atomicbitset

import (
	"sync"
	"testing"

	roaring "github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestHasRange(t *testing.T) {
	t.Parallel()
	b := New()
	b.SetRange(10, 20)

	require.True(t, b.HasRange(10, 20))
	require.True(t, b.HasRange(12, 18))
	require.False(t, b.HasRange(9, 20))
	require.False(t, b.HasRange(10, 21))
	require.True(t, b.HasRange(50, 50))
}

func TestSetRange_Overlapping(t *testing.T) {
	t.Parallel()
	b := New()
	b.SetRange(5, 10)
	b.SetRange(8, 13)

	require.True(t, b.HasRange(5, 13))
	require.False(t, b.HasRange(4, 5))
	require.False(t, b.HasRange(13, 14))
}

func TestSetRange_Concurrent(t *testing.T) {
	t.Parallel()
	b := New()

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			lo := uint(g) * 32
			b.SetRange(lo, lo+128)
		})
	}
	wg.Wait()

	last := uint(7*32 + 128)
	require.True(t, b.HasRange(0, last))
	require.False(t, b.HasRange(last, last+1))
}

func TestHasRange_PastSetBits(t *testing.T) {
	t.Parallel()
	b := New()
	b.SetRange(0, 128)

	require.False(t, b.HasRange(200, 300))
	require.True(t, b.HasRange(128, 128))
	require.False(t, b.HasRange(128, 200))
}

// TestCachePattern reproduces the exact SetRange/HasRange sequence that the
// block cache uses: chunk-aligned writes followed by sub-block reads.
func TestCachePattern(t *testing.T) {
	t.Parallel()

	const (
		fileSize  int64 = 6_815_744 // 6.5 MB
		blockSize int64 = 4096
		chunkSize int64 = 4_194_304 // 4 MB
	)

	totalBlocks := uint((fileSize + blockSize - 1) / blockSize)
	startBlock := func(off int64) uint { return uint(off / blockSize) }
	endBlock := func(off int64) uint { return uint((off + blockSize - 1) / blockSize) }

	b := New()

	for fetchOff := int64(0); fetchOff < fileSize; fetchOff += chunkSize {
		readBytes := min(chunkSize, fileSize-fetchOff)
		b.SetRange(startBlock(fetchOff), endBlock(fetchOff+readBytes))
	}

	require.True(t, b.HasRange(0, totalBlocks))

	for off := int64(0); off < fileSize; off += blockSize {
		lo := startBlock(off)
		hi := endBlock(min(off+blockSize, fileSize))
		require.True(t, b.HasRange(lo, hi),
			"HasRange(%d, %d) false for offset %d", lo, hi, off)
	}
}

// TestHasRange_GapBetweenRanges is a regression test for a bug in
// roaring.Bitmap.CardinalityInRange (v2.16.0). Our HasRange uses
// Rank() which does not have this bug.
func TestHasRange_GapBetweenRanges(t *testing.T) {
	t.Parallel()
	b := New()
	b.SetRange(0, 1024)
	b.SetRange(2048, 3072)

	require.True(t, b.HasRange(0, 1024))
	require.True(t, b.HasRange(2048, 3072))
	require.False(t, b.HasRange(1024, 2048), "gap must not be reported as set")
	require.False(t, b.HasRange(0, 3072), "span across gap must not be reported as set")
	require.False(t, b.HasRange(1023, 1025), "crossing into gap must not be reported as set")
}

// TestCardinalityInRange_GapFix verifies the fix for an off-by-one in
// roaring v2.16.0 runContainer16.getCardinalityInRange (line 2546).
// We use e2b-dev/roaring which includes the fix (upstream PR #521).
// If this test breaks after switching back to upstream, the fix was
// reverted or not yet merged.
func TestCardinalityInRange_GapFix(t *testing.T) {
	t.Parallel()
	bm := roaring.New()
	bm.AddRange(0, 1024)
	bm.AddRange(2048, 3072)

	require.Equal(t, uint64(0), bm.CardinalityInRange(1024, 2048),
		"gap between runs must have zero cardinality")
}

// --- Benchmarks ---
//
// 1 GB rootfs / 4 KB blocks = 262144 bits, 4 MB chunks = 1024 blocks/chunk.
const (
	benchBits  uint = 262144
	benchChunk uint = 1024
)

func BenchmarkSetRange(b *testing.B) {
	bs := New()
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / benchChunk) * benchChunk
		bs.SetRange(lo, lo+benchChunk)
	}
}

func BenchmarkHasRange_Hit(b *testing.B) {
	bs := New()
	bs.SetRange(0, benchBits)
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / benchChunk) * benchChunk
		if !bs.HasRange(lo, lo+benchChunk) {
			b.Fatal("expected set")
		}
	}
}

func BenchmarkHasRange_Miss(b *testing.B) {
	bs := New()
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / benchChunk) * benchChunk
		if bs.HasRange(lo, lo+benchChunk) {
			b.Fatal("expected unset")
		}
	}
}

func BenchmarkHasRange_HitConcurrent(b *testing.B) {
	bs := New()
	bs.SetRange(0, benchBits)
	b.SetParallelism(16)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := uint(0)
		for pb.Next() {
			lo := i % (benchBits / benchChunk) * benchChunk
			if !bs.HasRange(lo, lo+benchChunk) {
				b.Fatal("expected set")
			}
			i++
		}
	})
}

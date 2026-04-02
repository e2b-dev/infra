package atomicbitset

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type implFactory struct {
	name string
	make func(n uint) Bitset
}

var impls = []implFactory{
	{"Flat", func(n uint) Bitset { return NewFlat(n) }},
	{"Roaring", func(n uint) Bitset { return NewRoaring(n) }},
	{"Sharded", func(n uint) Bitset { return NewSharded(n, DefaultShardBits) }},
	{"Sharded/small", func(n uint) Bitset { return NewSharded(n, 64) }},
}

func TestHasRange(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(10, 20)

			require.True(t, b.HasRange(10, 20))
			require.True(t, b.HasRange(12, 18))
			require.False(t, b.HasRange(9, 20))
			require.False(t, b.HasRange(10, 21))
			require.True(t, b.HasRange(50, 50)) // empty
		})
	}
}

func TestSetRange_WordBoundary(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(256)
			b.SetRange(60, 68) // crosses word boundary at 64

			require.True(t, b.HasRange(60, 68))
			require.False(t, b.HasRange(59, 60))
			require.False(t, b.HasRange(68, 69))
		})
	}
}

func TestSetRange_Large(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(512)
			b.SetRange(50, 250)

			require.True(t, b.HasRange(50, 250))
			require.False(t, b.HasRange(49, 50))
			require.False(t, b.HasRange(250, 251))
		})
	}
}

func TestSetRange_All(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(256)
			b.SetRange(0, 256)

			require.True(t, b.HasRange(0, 256))
		})
	}
}

func TestSetRange_PastLen(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(126, 200) // should not panic, capped to 128

			require.True(t, b.HasRange(126, 128))
		})
	}
}

func TestSetRange_Idempotent(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(5, 6)
			b.SetRange(5, 6)

			require.Equal(t, []uint{5}, slices.Collect(b.Iterator()))
		})
	}
}

func TestSetRange_Overlapping(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(5, 10)
			b.SetRange(8, 13)

			require.True(t, b.HasRange(5, 13))
			require.False(t, b.HasRange(4, 5))
			require.False(t, b.HasRange(13, 14))
		})
	}
}

func TestSetRange_Concurrent(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(512)

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
		})
	}
}

func TestIterator_Empty(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			require.Empty(t, slices.Collect(b.Iterator()))
		})
	}
}

func TestIterator_Sorted(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(256)
			for _, i := range []uint{100, 5, 200, 63, 64} {
				b.SetRange(i, i+1)
			}
			require.Equal(t, []uint{5, 63, 64, 100, 200}, slices.Collect(b.Iterator()))
		})
	}
}

func TestIterator_Contiguous(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(10, 15)
			require.Equal(t, []uint{10, 11, 12, 13, 14}, slices.Collect(b.Iterator()))
		})
	}
}

func TestZero(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(0)
			require.Equal(t, uint(0), b.Len())
			require.Empty(t, slices.Collect(b.Iterator()))
			b.SetRange(0, 1) // should not panic
		})
	}
}

func TestSetRange_NonAligned(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(100) // not a multiple of 64
			b.SetRange(95, 100)

			require.True(t, b.HasRange(95, 100))
			require.True(t, b.Has(99))
			require.False(t, b.Has(94))
		})
	}
}

func TestHasRange_OutOfBounds(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(0, 128)

			require.False(t, b.HasRange(200, 300)) // lo past capacity
			require.True(t, b.HasRange(50, 50))    // empty, in range
			require.True(t, b.HasRange(128, 128))  // empty, at boundary
			require.False(t, b.HasRange(128, 200)) // lo at capacity, non-empty after cap
		})
	}
}

func TestLen(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(100)
			require.Equal(t, uint(100), b.Len())
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	require.IsType(t, (*Roaring)(nil), New(1000, ""))
	require.IsType(t, (*Roaring)(nil), New(1000, "roaring"))

	require.IsType(t, (*Flat)(nil), New(1000, "atomic"))
	require.IsType(t, (*Sharded)(nil), New(autoThreshold+1, "atomic"))

	require.Panics(t, func() { New(1000, "bogus") })
}

// --- Benchmarks ---

const benchBits uint = 16384

func benchmarkSetRange(b *testing.B, make func(uint) Bitset) {
	bs := make(benchBits)
	const chunk uint = 1024
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / chunk) * chunk
		bs.SetRange(lo, lo+chunk)
	}
}

func benchmarkHasRangeHit(b *testing.B, make func(uint) Bitset) {
	bs := make(benchBits)
	bs.SetRange(0, benchBits)
	const chunk uint = 1024
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / chunk) * chunk
		if !bs.HasRange(lo, lo+chunk) {
			b.Fatal("expected set")
		}
	}
}

func benchmarkHasRangeMiss(b *testing.B, make func(uint) Bitset) {
	bs := make(benchBits)
	const chunk uint = 1024
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / chunk) * chunk
		if bs.HasRange(lo, lo+chunk) {
			b.Fatal("expected unset")
		}
	}
}

func BenchmarkRoaringSetRange(b *testing.B) {
	benchmarkSetRange(b, func(n uint) Bitset { return NewRoaring(n) })
}

func BenchmarkRoaringHasRange_Hit(b *testing.B) {
	benchmarkHasRangeHit(b, func(n uint) Bitset { return NewRoaring(n) })
}

func BenchmarkRoaringHasRange_Miss(b *testing.B) {
	benchmarkHasRangeMiss(b, func(n uint) Bitset { return NewRoaring(n) })
}

func BenchmarkShardedSetRange(b *testing.B) {
	benchmarkSetRange(b, func(n uint) Bitset { return NewSharded(n, DefaultShardBits) })
}

func BenchmarkShardedHasRange_Hit(b *testing.B) {
	benchmarkHasRangeHit(b, func(n uint) Bitset { return NewSharded(n, DefaultShardBits) })
}

func BenchmarkShardedHasRange_Miss(b *testing.B) {
	benchmarkHasRangeMiss(b, func(n uint) Bitset { return NewSharded(n, DefaultShardBits) })
}

// --- Concurrent read benchmarks ---

var concurrencyLevels = []int{1, 4, 16, 64}

func benchmarkHasRangeHitConcurrent(b *testing.B, make func(uint) Bitset) {
	for _, p := range concurrencyLevels {
		b.Run(fmt.Sprintf("P%d", p), func(b *testing.B) {
			bs := make(benchBits)
			bs.SetRange(0, benchBits)
			const chunk uint = 1024

			b.SetParallelism(p)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := uint(0)
				for pb.Next() {
					lo := i % (benchBits / chunk) * chunk
					if !bs.HasRange(lo, lo+chunk) {
						b.Fatal("expected set")
					}
					i++
				}
			})
		})
	}
}

func BenchmarkFlatHasRange_HitConcurrent(b *testing.B) {
	benchmarkHasRangeHitConcurrent(b, func(n uint) Bitset { return NewFlat(n) })
}

func BenchmarkRoaringHasRange_HitConcurrent(b *testing.B) {
	benchmarkHasRangeHitConcurrent(b, func(n uint) Bitset { return NewRoaring(n) })
}

func BenchmarkShardedHasRange_HitConcurrent(b *testing.B) {
	benchmarkHasRangeHitConcurrent(b, func(n uint) Bitset { return NewSharded(n, DefaultShardBits) })
}

package atomicbitset

import (
	"fmt"
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
	{"Roaring64", func(n uint) Bitset { return NewRoaring64(n) }},
	{"BitsAndBlooms", func(n uint) Bitset { return NewBitsAndBlooms(n) }},
	{"Sharded", func(n uint) Bitset { return NewSharded(n, DefaultShardBits) }},
	{"Sharded/small", func(n uint) Bitset { return NewSharded(n, 64) }},
	{"SyncMap", func(n uint) Bitset { return NewSyncMap(n) }},
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

			require.True(t, b.Has(5))
			require.False(t, b.Has(4))
			require.False(t, b.Has(6))
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

	// All impls are safe for concurrent use (atomic via atomic ops, others via internal mutex).
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

func TestHas_Empty(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			for i := range uint(128) {
				require.False(t, b.Has(i), "bit %d should be unset", i)
			}
			require.False(t, b.HasRange(0, 128))
		})
	}
}

func TestHas_Individual(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(256)
			set := []uint{100, 5, 200, 63, 64}
			for _, i := range set {
				b.SetRange(i, i+1)
			}
			for _, i := range set {
				require.True(t, b.Has(i), "bit %d should be set", i)
			}
			for _, i := range []uint{0, 4, 6, 62, 65, 99, 101, 199, 201, 255} {
				require.False(t, b.Has(i), "bit %d should be unset", i)
			}
		})
	}
}

func TestHasRange_Contiguous(t *testing.T) {
	t.Parallel()
	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(128)
			b.SetRange(10, 15)
			require.True(t, b.HasRange(10, 15))
			require.False(t, b.Has(9))
			require.False(t, b.Has(15))
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
			require.False(t, b.Has(0))
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
	require.IsType(t, (*Roaring)(nil), New(autoThreshold+1, ""))
	require.IsType(t, (*Flat)(nil), New(1000, "atomic"))
	require.IsType(t, (*Sharded)(nil), New(autoThreshold+1, "atomic"))
	require.IsType(t, (*Roaring)(nil), New(1000, "roaring"))
	require.IsType(t, (*BitsAndBlooms)(nil), New(1000, "bits-and-blooms"))
	require.IsType(t, (*SyncMap)(nil), New(1000, "syncmap"))

	require.Panics(t, func() { New(1000, "bogus") })
}

// TestCachePattern reproduces the exact SetRange/HasRange sequence that the
// block cache uses: chunk-aligned writes followed by arbitrary sub-block reads.
// Parameters mirror a real 6.5 MB rootfs with 4 KB blocks and 4 MB chunks.
func TestCachePattern(t *testing.T) {
	t.Parallel()

	const (
		fileSize  int64 = 6_815_744 // bytes
		blockSize int64 = 4096      // bytes
		chunkSize int64 = 4_194_304 // 4 MB
	)

	totalBlocks := uint((fileSize + blockSize - 1) / blockSize) // ceil
	startBlock := func(off int64) uint { return uint(off / blockSize) }
	endBlock := func(off int64) uint { return uint((off + blockSize - 1) / blockSize) }

	for _, impl := range impls {
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			b := impl.make(totalBlocks)

			// Simulate chunk fetcher writing all chunks.
			for fetchOff := int64(0); fetchOff < fileSize; fetchOff += chunkSize {
				readBytes := min(chunkSize, fileSize-fetchOff)
				lo := startBlock(fetchOff)
				hi := endBlock(fetchOff + readBytes)
				b.SetRange(lo, hi)
			}

			// Every individual block should now be cached.
			for blk := range totalBlocks {
				require.True(t, b.Has(blk), "block %d not set", blk)
			}

			// Full-range check.
			require.True(t, b.HasRange(0, totalBlocks), "full range not set")

			// Simulate NBD reads: 4K-aligned reads across the entire file.
			for off := int64(0); off < fileSize; off += blockSize {
				end := min(off+blockSize, fileSize)
				lo := startBlock(off)
				hi := endBlock(end)
				require.True(t, b.HasRange(lo, hi),
					"HasRange(%d, %d) false for read at offset %d", lo, hi, off)
			}

			// Simulate isCached(fetchOff, MemoryChunkSize) — the exact call
			// the chunker makes to check whether a chunk is already cached.
			for fetchOff := int64(0); fetchOff < fileSize; fetchOff += chunkSize {
				end := min(fetchOff+chunkSize, fileSize)
				lo := startBlock(fetchOff)
				hi := endBlock(end)
				require.True(t, b.HasRange(lo, hi),
					"chunk HasRange(%d, %d) false for fetchOff %d", lo, hi, fetchOff)
			}
		})
	}
}

// --- Benchmarks ---
//
// Realistic sizes:
//   rootfs:  512 MB / 4 KB blocks  = 131072 bits
//   memfile: 2 GB  / 2 MB hugepages = 1024 bits

// Realistic size: 1 GB rootfs / 4 KB blocks = 262144 bits.
// Chunk = 1024 blocks (4 MB chunk at 4 KB block size).
// Sharded: DefaultShardBits=32768 → 8 shards (128 MB / 4 KB each).
const (
	benchBits  uint = 262144
	benchChunk uint = 1024
)

// benchImpls excludes Sharded/small (not a production config).
var benchImpls = []implFactory{
	{"Flat", func(n uint) Bitset { return NewFlat(n) }},
	{"Roaring", func(n uint) Bitset { return NewRoaring(n) }},
	{"BitsAndBlooms", func(n uint) Bitset { return NewBitsAndBlooms(n) }},
	{"Sharded", func(n uint) Bitset { return NewSharded(n, DefaultShardBits) }},
}

func BenchmarkSetRange(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			bs := impl.make(benchBits)
			b.ResetTimer()
			for i := range b.N {
				lo := uint(i) % (benchBits / benchChunk) * benchChunk
				bs.SetRange(lo, lo+benchChunk)
			}
		})
	}
}

func BenchmarkHas_Hit(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			bs := impl.make(benchBits)
			bs.SetRange(0, benchBits)
			b.ResetTimer()
			for i := range b.N {
				bit := uint(i) % benchBits
				if !bs.Has(bit) {
					b.Fatal("expected set")
				}
			}
		})
	}
}

func BenchmarkHas_Miss(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			bs := impl.make(benchBits)
			b.ResetTimer()
			for i := range b.N {
				bit := uint(i) % benchBits
				if bs.Has(bit) {
					b.Fatal("expected unset")
				}
			}
		})
	}
}

func BenchmarkHasRange_Hit(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			bs := impl.make(benchBits)
			bs.SetRange(0, benchBits)
			b.ResetTimer()
			for i := range b.N {
				lo := uint(i) % (benchBits / benchChunk) * benchChunk
				if !bs.HasRange(lo, lo+benchChunk) {
					b.Fatal("expected set")
				}
			}
		})
	}
}

func BenchmarkHasRange_Miss(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			bs := impl.make(benchBits)
			b.ResetTimer()
			for i := range b.N {
				lo := uint(i) % (benchBits / benchChunk) * benchChunk
				if bs.HasRange(lo, lo+benchChunk) {
					b.Fatal("expected unset")
				}
			}
		})
	}
}

// --- Concurrent read benchmarks ---

var concurrencyLevels = []int{1, 4, 16, 64}

func BenchmarkHas_HitConcurrent(b *testing.B) {
	for _, impl := range benchImpls {
		b.Run(impl.name, func(b *testing.B) {
			for _, p := range concurrencyLevels {
				b.Run(fmt.Sprintf("P%d", p), func(b *testing.B) {
					bs := impl.make(benchBits)
					bs.SetRange(0, benchBits)

					b.SetParallelism(p)
					b.ResetTimer()
					b.RunParallel(func(pb *testing.PB) {
						i := uint(0)
						for pb.Next() {
							bit := i % benchBits
							if !bs.Has(bit) {
								b.Fatal("expected set")
							}
							i++
						}
					})
				})
			}
		})
	}
}

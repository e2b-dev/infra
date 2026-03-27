package atomicbitset

import (
	"slices"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHas(t *testing.T) {
	t.Parallel()
	b := New(128)

	require.False(t, b.Has(0))
	b.SetRange(0, 1)
	require.True(t, b.Has(0))
	require.False(t, b.Has(1))
}

func TestHas_OutOfRange(t *testing.T) {
	t.Parallel()
	b := New(64)
	require.False(t, b.Has(64))
	require.False(t, b.Has(1000))
}

func TestHasRange(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(10, 20)

	require.True(t, b.HasRange(10, 20))
	require.True(t, b.HasRange(12, 18))
	require.False(t, b.HasRange(9, 20))
	require.False(t, b.HasRange(10, 21))
	require.True(t, b.HasRange(50, 50)) // empty
}

func TestSetRange(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(2, 6)

	for i := uint(2); i < 6; i++ {
		require.True(t, b.Has(i), "bit %d", i)
	}
	require.False(t, b.Has(0))
	require.False(t, b.Has(1))
	require.False(t, b.Has(6))
}

func TestSetRange_WordBoundary(t *testing.T) {
	t.Parallel()
	b := New(256)
	b.SetRange(60, 68) // crosses word boundary at 64

	for i := uint(60); i < 68; i++ {
		require.True(t, b.Has(i), "bit %d", i)
	}
	require.False(t, b.Has(59))
	require.False(t, b.Has(68))
}

func TestSetRange_Large(t *testing.T) {
	t.Parallel()
	b := New(512)
	b.SetRange(50, 250)

	for i := uint(50); i < 250; i++ {
		require.True(t, b.Has(i), "bit %d", i)
	}
	require.False(t, b.Has(49))
	require.False(t, b.Has(250))
}

func TestSetRange_First(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(0, 1)
	require.True(t, b.Has(0))
	require.False(t, b.Has(1))
}

func TestSetRange_Last(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(127, 128)
	require.True(t, b.Has(127))
	require.False(t, b.Has(126))
}

func TestSetRange_All(t *testing.T) {
	t.Parallel()
	b := New(256)
	b.SetRange(0, 256)

	for i := range uint(256) {
		require.True(t, b.Has(i), "bit %d", i)
	}
}

func TestSetRange_PastLen(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(126, 200) // should not panic

	require.True(t, b.Has(126))
	require.True(t, b.Has(127))
}

func TestSetRange_Idempotent(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(5, 6)
	b.SetRange(5, 6)

	require.True(t, b.Has(5))
	require.Equal(t, []uint{5}, slices.Collect(b.Iterator()))
}

func TestSetRange_Overlapping(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(5, 10)
	b.SetRange(8, 13)

	for i := uint(5); i <= 12; i++ {
		require.True(t, b.Has(i), "bit %d", i)
	}
	require.False(t, b.Has(4))
	require.False(t, b.Has(13))
}

func TestSetRange_Concurrent(t *testing.T) {
	t.Parallel()
	b := New(512)

	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			lo := uint(g) * 32
			b.SetRange(lo, lo+128)
		})
	}
	wg.Wait()

	last := uint(7*32 + 128)
	for i := range last {
		require.True(t, b.Has(i), "bit %d", i)
	}
	require.False(t, b.Has(last))
}

func TestIterator_Empty(t *testing.T) {
	t.Parallel()
	b := New(128)
	require.Empty(t, slices.Collect(b.Iterator()))
}

func TestIterator_Sorted(t *testing.T) {
	t.Parallel()
	b := New(256)
	for _, i := range []uint{100, 5, 200, 63, 64} {
		b.SetRange(i, i+1)
	}
	require.Equal(t, []uint{5, 63, 64, 100, 200}, slices.Collect(b.Iterator()))
}

func TestIterator_Contiguous(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(10, 15)
	require.Equal(t, []uint{10, 11, 12, 13, 14}, slices.Collect(b.Iterator()))
}

func TestNew_Zero(t *testing.T) {
	t.Parallel()
	b := New(0)
	require.Equal(t, uint(0), b.Len())
	require.False(t, b.Has(0))
	require.Empty(t, slices.Collect(b.Iterator()))
	b.SetRange(0, 1) // should not panic
}

func TestHasRange_OutOfBounds(t *testing.T) {
	t.Parallel()
	b := New(128)
	b.SetRange(0, 128)

	require.False(t, b.HasRange(200, 300)) // lo past capacity
	require.True(t, b.HasRange(50, 50))    // empty, in range
	require.True(t, b.HasRange(128, 128))  // empty, at boundary
	require.False(t, b.HasRange(128, 200)) // lo at capacity, non-empty after cap
}

func TestLen(t *testing.T) {
	t.Parallel()
	b := New(100)
	require.Equal(t, uint(100), b.Len())
}

// --- Benchmarks ---

const benchBits uint = 16384

func BenchmarkSetRange(b *testing.B) {
	bs := New(benchBits)
	const chunk uint = 1024
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / chunk) * chunk
		bs.SetRange(lo, lo+chunk)
	}
}

func BenchmarkHasRange_Hit(b *testing.B) {
	bs := New(benchBits)
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

func BenchmarkHasRange_Miss(b *testing.B) {
	bs := New(benchBits)
	const chunk uint = 1024
	b.ResetTimer()
	for i := range b.N {
		lo := uint(i) % (benchBits / chunk) * chunk
		if bs.HasRange(lo, lo+chunk) {
			b.Fatal("expected unset")
		}
	}
}

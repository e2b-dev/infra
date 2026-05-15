package header

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestUpsampleBitmap_RatioOneClones(t *testing.T) {
	t.Parallel()

	src := roaring.New()
	src.Add(3)
	src.Add(7)

	got := UpsampleBitmap(src, 1)

	require.True(t, src.Equals(got))
	// Mutating the result must not affect the source.
	got.Add(42)
	require.False(t, src.Contains(42))
}

func TestUpsampleBitmap_ExpandsEachBit(t *testing.T) {
	t.Parallel()

	src := roaring.New()
	src.Add(0)
	src.Add(2)

	got := UpsampleBitmap(src, 4)

	// Bit 0 expands to [0,4); bit 2 expands to [8,12); bits [4,8) stay clear.
	want := roaring.New()
	want.AddRange(0, 4)
	want.AddRange(8, 12)

	require.True(t, want.Equals(got))
}

func TestUpsampleBitmap_EmptyInputStaysEmpty(t *testing.T) {
	t.Parallel()

	got := UpsampleBitmap(roaring.New(), 512)

	require.Zero(t, got.GetCardinality())
}

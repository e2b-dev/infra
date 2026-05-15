package header

import "github.com/RoaringBitmap/roaring/v2"

// UpsampleBitmap returns a new bitmap where each set bit at index i in src is
// expanded to ratio consecutive bits starting at i*ratio.
//
// Used to convert a coarser-granularity bitmap (e.g. one bit per 2 MiB block)
// into a finer-granularity one (e.g. one bit per 4 KiB page) when the coarse
// block represents a uniform property of all the pages inside it — the
// canonical case being an Empty (all-zero) block, which implies every page
// within is also all-zero.
//
// ratio must be ≥ 1. ratio == 1 returns a clone.
func UpsampleBitmap(src *roaring.Bitmap, ratio uint64) *roaring.Bitmap {
	if ratio == 1 {
		return src.Clone()
	}

	out := roaring.New()
	iter := src.Iterator()
	for iter.HasNext() {
		idx := uint64(iter.Next())
		out.AddRange(idx*ratio, (idx+1)*ratio)
	}

	return out
}

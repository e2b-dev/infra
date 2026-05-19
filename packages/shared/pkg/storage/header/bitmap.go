package header

import "github.com/RoaringBitmap/roaring/v2"

// UpsampleBitmap returns a new bitmap where each set bit at index i in src is
// expanded to ratio consecutive bits starting at i*ratio. ratio must be ≥ 1.
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

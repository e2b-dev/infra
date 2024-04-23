package block

type Marker interface {
	// Mark the block at specified offset.
	Mark(off int64)
}

type Checker interface {
	// Check if the block at specified offset is marked.
	IsMarked(off int64) bool
}

// We may want to use a different (compressed) bitset implementation, hash maps or trees later, based on the performance.
// https://github.com/RoaringBitmap/roaring
// https://github.com/bits-and-blooms/bitset
type Tracker interface {
	Marker
	Checker
}

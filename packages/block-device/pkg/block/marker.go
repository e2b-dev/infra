package block

// We may want to use a different (compressed) bitset implementation, hash maps or trees later, based on the performance.
// https://github.com/RoaringBitmap/roaring
// https://github.com/bits-and-blooms/bitset
type Marker interface {
	// Check if the block at specified offset is marked.
	IsMarked(off int64) bool
	// Mark the block at specified offset.
	Mark(off int64)
}

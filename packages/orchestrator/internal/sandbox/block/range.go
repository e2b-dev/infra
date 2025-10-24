package block

type Range struct {
	// Start is the start address of the range in bytes.
	// Start is inclusive.
	Start int64
	// Size is the size of the range in bytes.
	Size int64
}

// NewRange creates a new range from a start address and size in bytes.
func NewRange(start, size int64) Range {
	return Range{
		Start: start,
		Size:  size,
	}
}

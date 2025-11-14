package testutils

// FirstDifferentByte returns the first byte index where a and b differ.
// It also returns the differing byte values (want, got).
// If slices are identical, it returns idx -1.
func FirstDifferentByte(a, b []byte) (idx int, want, got byte) {
	smallerSize := min(len(a), len(b))

	for i := range smallerSize {
		if a[i] != b[i] {
			return i, a[i], b[i]
		}
	}

	if len(a) != len(b) {
		return smallerSize, 0, 0
	}

	return -1, 0, 0
}

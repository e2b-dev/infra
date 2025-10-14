package testutils

import (
	"crypto/rand"
)

func RandomPages(pagesize, numberOfPages uint64) *contentSlicer {
	size := pagesize * numberOfPages

	n := int(size)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return newContentSlicer(buf, int64(pagesize))
}

// DiffByte returns the first byte index where a and b differ.
// It also returns the differing byte values (want, got).
// If slices are identical, it returns -1.
func DiffByte(a, b []byte) (idx int, want, got byte) {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := range minLen {
		if a[i] != b[i] {
			return i, b[i], a[i]
		}
	}

	if len(a) != len(b) {
		return minLen, 0, 0
	}

	return -1, 0, 0
}

package testutils

import (
	"crypto/rand"
)

func GenerateTestData(pagesize, pagesInTestData uint64) (data *mockSlicer, size uint64) {
	size = pagesize * pagesInTestData

	n := int(size)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	data = newMockSlicer(
		buf,
	)

	return data, size
}

// DiffByte returns the first byte index where a and b differ.
// It also returns the differing byte values (want, got).
// If slices are identical, it returns -1.
func DiffByte(a, b []byte) (idx int, want, got byte) {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] != b[i] {
			return i, b[i], a[i]
		}
	}
	if len(a) != len(b) {
		return minLen, 0, 0
	}
	return -1, 0, 0
}

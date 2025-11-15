package testutils

import (
	"crypto/rand"
)

func RandomPages(pagesize, numberOfPages uint64) *MemorySlicer {
	size := pagesize * numberOfPages

	n := int(size)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return NewMemorySlicer(buf, int64(pagesize))
}

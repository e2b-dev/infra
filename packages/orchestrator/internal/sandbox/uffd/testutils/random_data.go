package testutils

import (
	"crypto/rand"
)

func RandomPages(pagesize, numberOfPages uint64) *memorySlicer {
	size := pagesize * numberOfPages

	n := int(size)
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}

	return newMemorySlicer(buf, int64(pagesize))
}

package backend

import (
	"github.com/e2b-dev/infra/packages/block-device/internal/block"
)

type MemoryStorage struct {
	cache []byte
	dirty map[int64]struct{}
}

func NewMemoryStorage(size int64) *MemoryStorage {
	return &MemoryStorage{
		cache: make([]byte, size),
		dirty: make(map[int64]struct{}),
	}
}

func (c *MemoryStorage) isDirty(off int64) bool {
	_, ok := c.dirty[off]
	return ok
}

func (c *MemoryStorage) markDirty(off int64) {
	c.dirty[off] = struct{}{}
}

func (c *MemoryStorage) ReadAt(b []byte, off int64) (n int, err error) {
	if !c.isDirty(off) {
		return 0, block.ErrBytesNotAvailable{}
	}

	n = copy(b, c.cache[off:off+int64(len(b))])

	return n, nil
}

func (c *MemoryStorage) WriteAt(b []byte, off int64) (n int, err error) {
	n = copy(c.cache[off:off+int64(len(b))], b)

	c.markDirty(off)

	return n, nil
}

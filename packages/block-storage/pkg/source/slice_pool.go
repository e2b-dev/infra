package source

import "sync"

// For this use case we don't need to cleanup the slices' content, because we are overwriting them fully with data.
type slicePool struct {
	pool sync.Pool
}

func (c *slicePool) get() []byte {
	return c.pool.Get().([]byte)
}

func (c *slicePool) put(b []byte) {
	c.pool.Put(b)
}

func newSlicePool(size int64) *slicePool {
	return &slicePool{
		pool: sync.Pool{
			New: func() any {
				return make([]byte, size)
			},
		},
	}
}

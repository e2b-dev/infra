package storage

import "sync"

// bufferPool hands out reusable byte buffers backed by a sync.Pool. It is safe
// for concurrent use. Wrapping the pool lets callers create a ready-to-use
// (global) pool with a single newBufferPool call and keeps the get/put pairing
// owned by the pool and its inputBuf, so the pool is never touched directly.
type bufferPool struct {
	pool sync.Pool
}

// newBufferPool returns an empty buffer pool ready for use.
func newBufferPool() *bufferPool {
	return &bufferPool{}
}

// Get returns a buffer with len == size, reusing a pooled buffer when one with
// enough capacity is available. The size guard keeps it correct for any size.
func (p *bufferPool) Get(size int) inputBuf {
	if v := p.pool.Get(); v != nil {
		bufPtr := v.(*[]byte)
		if cap(*bufPtr) < size {
			// Grow in place so the pooled *[]byte is reused rather than
			// dropped; only the backing array is reallocated.
			*bufPtr = make([]byte, size)
		} else {
			*bufPtr = (*bufPtr)[:size]
		}

		return inputBuf{pool: p, ptr: bufPtr}
	}
	buf := make([]byte, size)

	return inputBuf{pool: p, ptr: &buf}
}

// inputBuf is a pooled buffer obtained from bufferPool.Get. Bytes exposes the
// backing slice and Free returns it to the owning pool; Free must be called
// exactly once, after the bytes are no longer read. Holding the pooled *[]byte
// (rather than the slice) keeps Free allocation-free.
type inputBuf struct {
	pool *bufferPool
	ptr  *[]byte
}

// Bytes returns the backing slice. It is only valid until Free is called.
func (b inputBuf) Bytes() []byte { return *b.ptr }

// Free returns the buffer to the owning pool. It must be called exactly once.
func (b inputBuf) Free() { b.pool.pool.Put(b.ptr) }

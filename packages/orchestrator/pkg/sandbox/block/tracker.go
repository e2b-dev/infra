package block

import (
	"iter"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Tracker struct {
	b  *roaring.Bitmap
	mu sync.RWMutex

	blockSize int64
}

func NewTracker(blockSize int64) *Tracker {
	return &Tracker{
		b:         roaring.New(),
		blockSize: blockSize,
	}
}

func (t *Tracker) Has(off int64) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.b.Contains(uint32(header.BlockIdx(off, t.blockSize)))
}

func (t *Tracker) Add(off int64) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.Add(uint32(header.BlockIdx(off, t.blockSize)))
}

func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.b.Clear()
}

func (t *Tracker) BlockSize() int64 {
	return t.blockSize
}

func (t *Tracker) Clone() *Tracker {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return &Tracker{
		b:         t.b.Clone(),
		blockSize: t.BlockSize(),
	}
}

func (t *Tracker) Offsets() iter.Seq[int64] {
	t.mu.RLock()
	defer t.mu.RUnlock()

	snapshot := t.b.Clone()

	return func(yield func(int64) bool) {
		snapshot.Iterate(func(idx uint32) bool {
			return yield(header.BlockOffset(int64(idx), t.blockSize))
		})
	}
}

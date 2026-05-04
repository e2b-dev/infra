package block

import (
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

type State uint8

const (
	// NotPresent: fall back to the previous layer.
	NotPresent State = iota
	// Dirty: this layer holds materialized data.
	Dirty
	// Zero: known-zero; no need to consult the previous layer.
	Zero
)

type Tracker struct {
	mu          sync.RWMutex
	dirty, zero *roaring.Bitmap
}

func NewTracker() *Tracker {
	return &Tracker{
		dirty: roaring.New(),
		zero:  roaring.New(),
	}
}

// SetRange takes uint64 to allow end = 1<<32 (roaring's half-open upper bound).
// Out-of-range values (end > 1<<32) are silently ignored; roaring is a 32-bit
// bitmap and AddRange panics otherwise.
func (t *Tracker) SetRange(start, end uint64, state State) {
	if end <= start || end > 1<<32 {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	switch state {
	case Dirty:
		t.dirty.AddRange(start, end)
		t.zero.RemoveRange(start, end)
	case Zero:
		t.zero.AddRange(start, end)
		t.dirty.RemoveRange(start, end)
	case NotPresent:
		t.dirty.RemoveRange(start, end)
		t.zero.RemoveRange(start, end)
	}
}

func (t *Tracker) Get(idx uint32) State {
	t.mu.RLock()
	defer t.mu.RUnlock()

	switch {
	case t.dirty.Contains(idx):
		return Dirty
	case t.zero.Contains(idx):
		return Zero
	default:
		return NotPresent
	}
}

// Export returns clones of the dirty and zero bitmaps.
func (t *Tracker) Export() (dirty, zero *roaring.Bitmap) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.dirty.Clone(), t.zero.Clone()
}

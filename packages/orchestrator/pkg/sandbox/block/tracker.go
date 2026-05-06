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

// SetRange sets state for indices in [start, end). The index math.MaxUint32
// is unaddressable: end is the half-open upper bound and capped at MaxUint32.
func (t *Tracker) SetRange(start, end uint32, state State) {
	if end <= start {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	s, e := uint64(start), uint64(end)
	switch state {
	case Dirty:
		t.dirty.AddRange(s, e)
		t.zero.RemoveRange(s, e)
	case Zero:
		t.zero.AddRange(s, e)
		t.dirty.RemoveRange(s, e)
	case NotPresent:
		t.dirty.RemoveRange(s, e)
		t.zero.RemoveRange(s, e)
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

// HasRange reports whether every index in [start, end) is in the given state.
// Only Dirty and Zero are accepted; passing NotPresent always returns false.
// Empty ranges (end == start) are vacuously true; inverted ranges return false.
func (t *Tracker) HasRange(start, end uint32, state State) bool {
	if end <= start {
		return end == start
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	var bm *roaring.Bitmap
	switch state {
	case Dirty:
		bm = t.dirty
	case Zero:
		bm = t.zero
	default:
		return false
	}

	return bm.CardinalityInRange(uint64(start), uint64(end)) == uint64(end-start)
}

// Present reports whether every index in [start, end) has been observed
// (i.e., is Dirty or Zero, not NotPresent).
func (t *Tracker) Present(start, end uint32) bool {
	if end <= start {
		return end == start
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	s, e := uint64(start), uint64(end)
	// Dirty and Zero are disjoint by invariant, so the cardinalities sum.
	return t.dirty.CardinalityInRange(s, e)+t.zero.CardinalityInRange(s, e) == e-s
}

// Export returns clones of the dirty and zero bitmaps.
func (t *Tracker) Export() (dirty, zero *roaring.Bitmap) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.dirty.Clone(), t.zero.Clone()
}

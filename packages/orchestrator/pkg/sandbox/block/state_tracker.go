package block

import (
	"fmt"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
)

type StateTracker[S comparable] struct {
	mu sync.RWMutex

	defaultState S
	a, b         S
	bmA, bmB     *roaring.Bitmap
}

// NewStateTracker requires three distinct states. Duplicates are a
// programming error — the switch in SetRange would silently favour the
// first matching case and corrupt bitmap state — so we reject them at
// construction rather than defer the bug to a later SetRange call.
func NewStateTracker[S comparable](defaultState, a, b S) (*StateTracker[S], error) {
	if defaultState == a || defaultState == b || a == b {
		return nil, fmt.Errorf("block.NewStateTracker: states must be distinct (default=%v a=%v b=%v)", defaultState, a, b)
	}

	return &StateTracker[S]{
		defaultState: defaultState,
		a:            a,
		b:            b,
		bmA:          roaring.New(),
		bmB:          roaring.New(),
	}, nil
}

// SetRange takes uint64 because roaring's range API allows end = 1<<32
// (the half-open upper bound of a 32-bit bitmap); Get stays uint32 since
// no 33-bit value can ever be a bitmap member.
func (t *StateTracker[S]) SetRange(start, end uint64, state S) error {
	if end <= start {
		return nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	switch state {
	case t.a:
		t.bmA.AddRange(start, end)
		t.bmB.RemoveRange(start, end)
	case t.b:
		t.bmB.AddRange(start, end)
		t.bmA.RemoveRange(start, end)
	case t.defaultState:
		t.bmA.RemoveRange(start, end)
		t.bmB.RemoveRange(start, end)
	default:
		// S is constrained only to comparable, so the compiler can't
		// prove exhaustiveness. A silent no-op here would hide a
		// programming error (caller added a state but forgot to wire
		// it); surfacing an error makes it fail fast in tests.
		return fmt.Errorf("block.StateTracker.SetRange: unsupported state %v (only default=%v a=%v b=%v allowed)", state, t.defaultState, t.a, t.b)
	}

	return nil
}

func (t *StateTracker[S]) Export() (a, b *roaring.Bitmap) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bmA.Clone(), t.bmB.Clone()
}

func (t *StateTracker[S]) Get(idx uint32) S {
	t.mu.RLock()
	defer t.mu.RUnlock()

	switch {
	case t.bmA.Contains(idx):
		return t.a
	case t.bmB.Contains(idx):
		return t.b
	default:
		return t.defaultState
	}
}

// HasRange reports whether every index in [start, end) is in some non-default
// state. The two bitmaps are disjoint, so their per-range cardinalities sum
// to the union cardinality without allocating.
func (t *StateTracker[S]) HasRange(start, end uint64) bool {
	if end <= start {
		return true
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	return t.bmA.CardinalityInRange(start, end)+t.bmB.CardinalityInRange(start, end) == end-start
}

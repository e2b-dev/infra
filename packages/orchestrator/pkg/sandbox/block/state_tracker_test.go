package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

type ts uint8

const (
	tsDefault ts = iota
	tsA
	tsB
)

// TestStateTracker exercises every transition pair (default↔a, default↔b,
// a↔b, idempotent same-state) and confirms the two non-default bitmaps
// stay disjoint.
func TestStateTracker(t *testing.T) {
	t.Parallel()

	t.Run("transitions", func(t *testing.T) {
		t.Parallel()
		s := NewStateTracker(tsDefault, tsA, tsB)

		s.SetRange(0, 1, tsA)
		assert.Equal(t, tsA, s.Get(0))

		s.SetRange(0, 1, tsB)
		assert.Equal(t, tsB, s.Get(0), "a→b should flip the page")
		bmA, bmB := s.Export()
		assert.False(t, bmA.Contains(0), "a→b must clear bmA")
		assert.True(t, bmB.Contains(0), "a→b must add to bmB")

		s.SetRange(0, 1, tsA)
		assert.Equal(t, tsA, s.Get(0), "b→a should flip back")

		s.SetRange(0, 1, tsDefault)
		assert.Equal(t, tsDefault, s.Get(0), "→default must clear")
		bmA, bmB = s.Export()
		assert.False(t, bmA.Contains(0))
		assert.False(t, bmB.Contains(0))

		s.SetRange(0, 1, tsA)
		s.SetRange(0, 1, tsA)
		assert.Equal(t, tsA, s.Get(0), "a→a is idempotent")
	})

	t.Run("partial overlap moves only the overlapping pages", func(t *testing.T) {
		t.Parallel()
		s := NewStateTracker(tsDefault, tsA, tsB)

		s.SetRange(0, 10, tsA)
		s.SetRange(3, 7, tsB)

		for i := range uint32(3) {
			assert.Equal(t, tsA, s.Get(i), "page %d outside overlap stays a", i)
		}
		for i := range uint32(4) {
			page := i + 3
			assert.Equal(t, tsB, s.Get(page), "page %d in overlap moves to b", page)
		}
		for i := range uint32(3) {
			page := i + 7
			assert.Equal(t, tsA, s.Get(page), "page %d outside overlap stays a", page)
		}
	})

	t.Run("empty and inverted ranges are no-ops", func(t *testing.T) {
		t.Parallel()
		s := NewStateTracker(tsDefault, tsA, tsB)

		s.SetRange(5, 5, tsA)
		s.SetRange(7, 3, tsB)
		bmA, bmB := s.Export()
		assert.True(t, bmA.IsEmpty())
		assert.True(t, bmB.IsEmpty())
	})

	t.Run("NewStateTracker rejects non-distinct states", func(t *testing.T) {
		t.Parallel()
		assert.Panics(t, func() { NewStateTracker(tsA, tsA, tsB) }, "default == a must panic")
		assert.Panics(t, func() { NewStateTracker(tsA, tsB, tsB) }, "a == b must panic")
		assert.Panics(t, func() { NewStateTracker(tsA, tsB, tsA) }, "default == b must panic")
		assert.Panics(t, func() { NewStateTracker(tsA, tsA, tsA) }, "all-equal must panic")
	})

	t.Run("SetRange panics on unsupported state", func(t *testing.T) {
		t.Parallel()
		s := NewStateTracker(tsDefault, tsA, tsB)
		assert.Panics(t, func() { s.SetRange(0, 1, ts(99)) },
			"unregistered state value must panic, not silently no-op")
	})
}

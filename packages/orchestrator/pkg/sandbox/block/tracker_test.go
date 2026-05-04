package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTracker(t *testing.T) {
	t.Parallel()

	t.Run("transitions", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 1, Dirty)
		assert.Equal(t, Dirty, s.Get(0))

		s.SetRange(0, 1, Zero)
		assert.Equal(t, Zero, s.Get(0), "dirty→zero should flip the page")
		bmDirty, bmZero := s.Export()
		assert.False(t, bmDirty.Contains(0), "dirty→zero must clear dirty bitmap")
		assert.True(t, bmZero.Contains(0), "dirty→zero must add to zero bitmap")

		s.SetRange(0, 1, Dirty)
		assert.Equal(t, Dirty, s.Get(0), "zero→dirty should flip back")

		s.SetRange(0, 1, NotPresent)
		assert.Equal(t, NotPresent, s.Get(0), "→not-present must clear")
		bmDirty, bmZero = s.Export()
		assert.False(t, bmDirty.Contains(0))
		assert.False(t, bmZero.Contains(0))

		s.SetRange(0, 1, Dirty)
		s.SetRange(0, 1, Dirty)
		assert.Equal(t, Dirty, s.Get(0), "dirty→dirty is idempotent")
	})

	t.Run("partial overlap moves only the overlapping pages", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 10, Dirty)
		s.SetRange(3, 7, Zero)

		for i := range uint32(3) {
			assert.Equal(t, Dirty, s.Get(i), "page %d outside overlap stays dirty", i)
		}
		for i := range uint32(4) {
			page := i + 3
			assert.Equal(t, Zero, s.Get(page), "page %d in overlap moves to zero", page)
		}
		for i := range uint32(3) {
			page := i + 7
			assert.Equal(t, Dirty, s.Get(page), "page %d outside overlap stays dirty", page)
		}
	})

	t.Run("empty and inverted ranges are no-ops", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(5, 5, Dirty)
		s.SetRange(7, 3, Zero)
		bmDirty, bmZero := s.Export()
		assert.True(t, bmDirty.IsEmpty())
		assert.True(t, bmZero.IsEmpty())
	})

	t.Run("Export returns clones", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 1, Dirty)
		bmDirty, _ := s.Export()
		bmDirty.Add(42)
		assert.Equal(t, NotPresent, s.Get(42), "mutating export must not leak into tracker")
	})
}

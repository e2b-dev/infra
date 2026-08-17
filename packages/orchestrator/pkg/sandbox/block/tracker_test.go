//go:build linux

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

	t.Run("removed transitions", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 1, Dirty)
		s.SetRange(0, 1, Removed)
		assert.Equal(t, Removed, s.Get(0), "dirty→removed should flip the page")
		bmDirty, bmEmpty := s.Export()
		assert.False(t, bmDirty.Contains(0), "dirty→removed must clear dirty bitmap")
		assert.True(t, bmEmpty.Contains(0), "removed pages export as empty")

		s.SetRange(0, 1, Zero)
		assert.Equal(t, Zero, s.Get(0), "removed→zero (zero-fill reinstall) should flip")
		_, bmEmpty = s.Export()
		assert.True(t, bmEmpty.Contains(0), "zero pages export as empty")

		s.SetRange(0, 1, Removed)
		s.SetRange(0, 1, Dirty)
		assert.Equal(t, Dirty, s.Get(0), "removed→dirty (write reinstall) should flip")
		bmDirty, bmEmpty = s.Export()
		assert.True(t, bmDirty.Contains(0))
		assert.False(t, bmEmpty.Contains(0), "dirty page must leave the empty set")

		s.SetRange(0, 1, Removed)
		s.SetRange(0, 1, NotPresent)
		assert.Equal(t, NotPresent, s.Get(0), "→not-present must clear removed")
		_, bmEmpty = s.Export()
		assert.False(t, bmEmpty.Contains(0))
	})

	t.Run("clean pages are present but export in neither set", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 3, Clean)
		assert.Equal(t, Clean, s.Get(0))
		assert.True(t, s.HasRange(0, 3, Clean))
		assert.True(t, s.Present(0, 3), "clean pages are installed")

		bmDirty, bmEmpty := s.Export()
		assert.True(t, bmDirty.IsEmpty(), "clean is not dirty: the diff inherits the page from the parent")
		assert.True(t, bmEmpty.IsEmpty(), "clean is not empty: the page holds source content")

		s.SetRange(0, 1, Dirty)
		assert.Equal(t, Dirty, s.Get(0), "clean→dirty promotion flips the page")
		bmDirty, _ = s.Export()
		assert.True(t, bmDirty.Contains(0))
	})

	t.Run("MarkInstalled only upgrades NotPresent and Removed", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 1, Dirty)
		s.SetRange(1, 2, Zero)
		s.SetRange(2, 3, Clean)
		s.SetRange(3, 4, Removed)
		// page 4 stays NotPresent

		s.MarkInstalled(0, 5, Clean)

		assert.Equal(t, Dirty, s.Get(0), "a late installer must not downgrade a promoted Dirty page")
		assert.Equal(t, Zero, s.Get(1), "installed zero page is untouched")
		assert.Equal(t, Clean, s.Get(2))
		assert.Equal(t, Clean, s.Get(3), "removed page adopts the reinstall state")
		assert.Equal(t, Clean, s.Get(4), "untracked page adopts the install state")

		s.SetRange(5, 6, Removed)
		s.MarkInstalled(5, 6, Zero)
		assert.Equal(t, Zero, s.Get(5), "zero-fill reinstall moves Removed back to Zero")

		s.MarkInstalled(6, 7, Dirty)
		assert.Equal(t, NotPresent, s.Get(6), "MarkInstalled only accepts read-side states")

		s.MarkInstalled(8, 8, Clean)
		assert.Equal(t, NotPresent, s.Get(8), "empty range is a no-op")
	})

	t.Run("MarkWritten promotion matrix", func(t *testing.T) {
		t.Parallel()

		cases := []struct {
			name     string
			setup    State // state to seed (NotPresent = leave untouched)
			wantPrev State
			want     State
		}{
			{"clean promotes", Clean, Clean, Dirty},
			{"zero promotes (written zero page must not export as empty)", Zero, Zero, Dirty},
			{"not-present promotes (installer mid-marking; page is present)", NotPresent, NotPresent, Dirty},
			{"dirty is a no-op", Dirty, Dirty, Dirty},
			{"removed is skipped (stale WP fault racing a REMOVE)", Removed, Removed, Removed},
		}
		for _, tc := range cases {
			s := NewTracker()
			if tc.setup != NotPresent {
				s.SetRange(0, 1, tc.setup)
			}

			prev := s.MarkWritten(0)
			assert.Equal(t, tc.wantPrev, prev, "%s: prev state", tc.name)
			assert.Equal(t, tc.want, s.Get(0), "%s: final state", tc.name)
		}
	})

	t.Run("MarkWritten then MarkInstalled keeps the promotion", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		// Installer suspended between UFFDIO_COPY and MarkInstalled while the
		// guest's write already went through a WP fault.
		s.MarkWritten(0)
		s.MarkInstalled(0, 1, Clean)
		assert.Equal(t, Dirty, s.Get(0), "late install marking must not downgrade the promotion")
	})

	t.Run("ExportStates returns the per-state split", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 1, Dirty)
		s.SetRange(1, 2, Zero)
		s.SetRange(2, 3, Removed)
		s.SetRange(3, 4, Clean)

		dirty, zero, removed, clean := s.ExportStates()
		assert.True(t, dirty.Contains(0))
		assert.True(t, zero.Contains(1))
		assert.True(t, removed.Contains(2))
		assert.True(t, clean.Contains(3))
		assert.EqualValues(t, 1, dirty.GetCardinality())
		assert.EqualValues(t, 1, zero.GetCardinality())
		assert.EqualValues(t, 1, removed.GetCardinality())
		assert.EqualValues(t, 1, clean.GetCardinality())
	})

	t.Run("zero and removed are distinct states but share the empty export", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 3, Zero)
		s.SetRange(3, 6, Removed)

		assert.True(t, s.HasRange(0, 3, Zero))
		assert.True(t, s.HasRange(3, 6, Removed))
		assert.False(t, s.HasRange(0, 6, Zero), "removed pages are not Zero")
		assert.False(t, s.HasRange(0, 6, Removed), "zero pages are not Removed")
		assert.True(t, s.Present(0, 6), "both states count as observed")

		bmDirty, bmEmpty := s.Export()
		assert.True(t, bmDirty.IsEmpty())
		assert.EqualValues(t, 6, bmEmpty.GetCardinality(), "empty set is zero ∪ removed")
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

	t.Run("HasRange checks contiguous state coverage", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 5, Dirty)
		s.SetRange(5, 10, Zero)

		assert.True(t, s.HasRange(0, 5, Dirty))
		assert.True(t, s.HasRange(5, 10, Zero))
		assert.False(t, s.HasRange(0, 5, Zero))
		assert.False(t, s.HasRange(0, 6, Dirty), "gap in dirty must fail")
		assert.True(t, s.HasRange(3, 3, Dirty), "empty range is vacuously true")
		assert.False(t, s.HasRange(7, 5, Dirty), "inverted range is false")
	})

	t.Run("Present accepts mixed Dirty and Zero", func(t *testing.T) {
		t.Parallel()
		s := NewTracker()

		s.SetRange(0, 5, Dirty)
		s.SetRange(5, 10, Zero)

		assert.True(t, s.Present(0, 10), "mixed Dirty+Zero should be present")
		assert.False(t, s.Present(0, 11), "any NotPresent in range fails")
		assert.True(t, s.Present(3, 3), "empty range is vacuously true")
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

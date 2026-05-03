package block

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ts uint8

const (
	tsDefault ts = iota
	tsA
	tsB
)

func TestStateTracker(t *testing.T) {
	t.Parallel()

	t.Run("transitions", func(t *testing.T) {
		t.Parallel()
		s, err := NewStateTracker(tsDefault, tsA, tsB)
		require.NoError(t, err)

		require.NoError(t, s.SetRange(0, 1, tsA))
		assert.Equal(t, tsA, s.Get(0))

		require.NoError(t, s.SetRange(0, 1, tsB))
		assert.Equal(t, tsB, s.Get(0), "a→b should flip the page")
		bmA, bmB := s.Export()
		assert.False(t, bmA.Contains(0), "a→b must clear bmA")
		assert.True(t, bmB.Contains(0), "a→b must add to bmB")

		require.NoError(t, s.SetRange(0, 1, tsA))
		assert.Equal(t, tsA, s.Get(0), "b→a should flip back")

		require.NoError(t, s.SetRange(0, 1, tsDefault))
		assert.Equal(t, tsDefault, s.Get(0), "→default must clear")
		bmA, bmB = s.Export()
		assert.False(t, bmA.Contains(0))
		assert.False(t, bmB.Contains(0))

		require.NoError(t, s.SetRange(0, 1, tsA))
		require.NoError(t, s.SetRange(0, 1, tsA))
		assert.Equal(t, tsA, s.Get(0), "a→a is idempotent")
	})

	t.Run("partial overlap moves only the overlapping pages", func(t *testing.T) {
		t.Parallel()
		s, err := NewStateTracker(tsDefault, tsA, tsB)
		require.NoError(t, err)

		require.NoError(t, s.SetRange(0, 10, tsA))
		require.NoError(t, s.SetRange(3, 7, tsB))

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
		s, err := NewStateTracker(tsDefault, tsA, tsB)
		require.NoError(t, err)

		require.NoError(t, s.SetRange(5, 5, tsA))
		require.NoError(t, s.SetRange(7, 3, tsB))
		bmA, bmB := s.Export()
		assert.True(t, bmA.IsEmpty())
		assert.True(t, bmB.IsEmpty())
	})

	t.Run("NewStateTracker errors on non-distinct states", func(t *testing.T) {
		t.Parallel()
		_, err := NewStateTracker(tsA, tsA, tsB)
		require.Error(t, err, "default == a must error")
		_, err = NewStateTracker(tsA, tsB, tsB)
		require.Error(t, err, "a == b must error")
		_, err = NewStateTracker(tsA, tsB, tsA)
		require.Error(t, err, "default == b must error")
		_, err = NewStateTracker(tsA, tsA, tsA)
		require.Error(t, err, "all-equal must error")
	})

	t.Run("SetRange errors on unsupported state", func(t *testing.T) {
		t.Parallel()
		s, err := NewStateTracker(tsDefault, tsA, tsB)
		require.NoError(t, err)
		require.Error(t, s.SetRange(0, 1, ts(99)),
			"unregistered state value must error, not silently no-op")
	})
}

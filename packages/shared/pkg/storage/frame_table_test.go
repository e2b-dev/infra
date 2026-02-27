package storage

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// threeFrameFT returns a FrameTable with three 1MB uncompressed frames
// and varying compressed sizes, starting at the given offset.
func threeFrameFT(startU, startC int64) *FrameTable {
	return &FrameTable{
		CompressionType: CompressionLZ4,
		StartAt:         FrameOffset{U: startU, C: startC},
		Frames: []FrameSize{
			{U: 1 << 20, C: 500_000}, // frame 0
			{U: 1 << 20, C: 600_000}, // frame 1
			{U: 1 << 20, C: 400_000}, // frame 2
		},
	}
}

// collectRange calls ft.Range and returns the offsets visited.
func collectRange(ft *FrameTable, start, length int64) ([]FrameOffset, error) {
	var offsets []FrameOffset
	err := ft.Range(start, length, func(offset FrameOffset, _ FrameSize) error {
		offsets = append(offsets, offset)

		return nil
	})

	return offsets, err
}

func TestRange(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)

	t.Run("selects all frames", func(t *testing.T) {
		t.Parallel()
		offsets, err := collectRange(ft, 0, 3<<20)
		require.NoError(t, err)
		assert.Len(t, offsets, 3)
	})

	t.Run("selects single middle frame", func(t *testing.T) {
		t.Parallel()
		offsets, err := collectRange(ft, 1<<20, 1<<20)
		require.NoError(t, err)
		require.Len(t, offsets, 1)
		assert.Equal(t, int64(1<<20), offsets[0].U)
		assert.Equal(t, int64(500_000), offsets[0].C)
	})

	t.Run("partial overlap selects touched frames", func(t *testing.T) {
		t.Parallel()
		// 1 byte spanning frames 0 and 1 boundary.
		offsets, err := collectRange(ft, (1<<20)-1, 2)
		require.NoError(t, err)
		assert.Len(t, offsets, 2)
	})

	t.Run("beyond end returns nothing", func(t *testing.T) {
		t.Parallel()
		offsets, err := collectRange(ft, 3<<20, 1)
		require.NoError(t, err)
		assert.Empty(t, offsets)
	})

	t.Run("callback error propagates", func(t *testing.T) {
		t.Parallel()
		sentinel := fmt.Errorf("stop")
		err := ft.Range(0, 3<<20, func(_ FrameOffset, _ FrameSize) error {
			return sentinel
		})
		assert.ErrorIs(t, err, sentinel)
	})

	t.Run("respects StartAt on subset", func(t *testing.T) {
		t.Parallel()
		sub, err := ft.Subset(Range{Start: 1 << 20, Length: 2 << 20})
		require.NoError(t, err)

		// Query for offset 2MB — the second frame of the subset.
		offsets, err := collectRange(sub, 2<<20, 1<<20)
		require.NoError(t, err)
		require.Len(t, offsets, 1)
		assert.Equal(t, int64(2<<20), offsets[0].U)
		assert.Equal(t, int64(1_100_000), offsets[0].C) // 500k + 600k

		// Query for offset 0 — before the subset, should find nothing.
		offsets, err = collectRange(sub, 0, 1<<20)
		require.NoError(t, err)
		assert.Empty(t, offsets, "Range should not find frames before StartAt")
	})
}

func TestSubset(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)

	t.Run("full range", func(t *testing.T) {
		t.Parallel()
		sub, err := ft.Subset(Range{Start: 0, Length: 3 << 20})
		require.NoError(t, err)
		assert.Len(t, sub.Frames, 3)
		assert.Equal(t, int64(0), sub.StartAt.U)
	})

	t.Run("last frame", func(t *testing.T) {
		t.Parallel()
		sub, err := ft.Subset(Range{Start: 2 << 20, Length: 1 << 20})
		require.NoError(t, err)
		require.Len(t, sub.Frames, 1)
		assert.Equal(t, int64(2<<20), sub.StartAt.U)
		assert.Equal(t, int64(1_100_000), sub.StartAt.C)
		assert.Equal(t, int32(400_000), sub.Frames[0].C)
	})

	t.Run("preserves compression type", func(t *testing.T) {
		t.Parallel()
		sub, err := ft.Subset(Range{Start: 0, Length: 1 << 20})
		require.NoError(t, err)
		assert.Equal(t, CompressionLZ4, sub.CompressionType)
	})

	t.Run("nil table returns nil", func(t *testing.T) {
		t.Parallel()
		sub, err := (*FrameTable)(nil).Subset(Range{Start: 0, Length: 100})
		require.NoError(t, err)
		assert.Nil(t, sub)
	})

	t.Run("zero length returns nil", func(t *testing.T) {
		t.Parallel()
		sub, err := ft.Subset(Range{Start: 0, Length: 0})
		require.NoError(t, err)
		assert.Nil(t, sub)
	})

	t.Run("before StartAt errors", func(t *testing.T) {
		t.Parallel()
		sub := threeFrameFT(1<<20, 500_000)
		_, err := sub.Subset(Range{Start: 0, Length: 1 << 20})
		assert.Error(t, err)
	})

	t.Run("beyond end errors", func(t *testing.T) {
		t.Parallel()
		_, err := ft.Subset(Range{Start: 4 << 20, Length: 1 << 20})
		assert.Error(t, err)
	})
}

func TestFrameFor(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)

	t.Run("first byte of each frame", func(t *testing.T) {
		t.Parallel()
		for i, wantU := range []int64{0, 1 << 20, 2 << 20} {
			start, size, err := ft.FrameFor(wantU)
			require.NoError(t, err, "frame %d", i)
			assert.Equal(t, wantU, start.U)
			assert.Equal(t, int32(1<<20), size.U)
		}
	})

	t.Run("last byte of frame", func(t *testing.T) {
		t.Parallel()
		start, _, err := ft.FrameFor((1 << 20) - 1)
		require.NoError(t, err)
		assert.Equal(t, int64(0), start.U)
	})

	t.Run("returns correct C offset", func(t *testing.T) {
		t.Parallel()
		start, _, err := ft.FrameFor(2 << 20)
		require.NoError(t, err)
		assert.Equal(t, int64(1_100_000), start.C) // 500k + 600k
	})

	t.Run("beyond end errors", func(t *testing.T) {
		t.Parallel()
		_, _, err := ft.FrameFor(3 << 20)
		assert.Error(t, err)
	})

	t.Run("nil table errors", func(t *testing.T) {
		t.Parallel()
		_, _, err := (*FrameTable)(nil).FrameFor(0)
		assert.Error(t, err)
	})

	t.Run("respects StartAt", func(t *testing.T) {
		t.Parallel()
		sub := threeFrameFT(1<<20, 500_000)
		start, _, err := sub.FrameFor(1 << 20)
		require.NoError(t, err)
		assert.Equal(t, int64(1<<20), start.U)
		assert.Equal(t, int64(500_000), start.C)

		// Before StartAt — no frame should contain offset 0.
		_, _, err = sub.FrameFor(0)
		assert.Error(t, err)
	})
}

func TestGetFetchRange(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)

	t.Run("translates U-space to C-space", func(t *testing.T) {
		t.Parallel()
		r, err := ft.GetFetchRange(Range{Start: 1 << 20, Length: 1 << 20})
		require.NoError(t, err)
		assert.Equal(t, int64(500_000), r.Start)
		assert.Equal(t, 600_000, r.Length)
	})

	t.Run("range spanning multiple frames errors", func(t *testing.T) {
		t.Parallel()
		_, err := ft.GetFetchRange(Range{Start: 0, Length: 2 << 20})
		assert.Error(t, err)
	})

	t.Run("nil table returns input unchanged", func(t *testing.T) {
		t.Parallel()
		input := Range{Start: 42, Length: 100}
		r, err := (*FrameTable)(nil).GetFetchRange(input)
		require.NoError(t, err)
		assert.Equal(t, input, r)
	})

	t.Run("uncompressed table returns input unchanged", func(t *testing.T) {
		t.Parallel()
		uncompressed := &FrameTable{CompressionType: CompressionNone}
		input := Range{Start: 42, Length: 100}
		r, err := uncompressed.GetFetchRange(input)
		require.NoError(t, err)
		assert.Equal(t, input, r)
	})
}

func TestSize(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)
	u, c := ft.Size()
	assert.Equal(t, int64(3<<20), u)
	assert.Equal(t, int64(1_500_000), c)
}

func TestIsCompressed(t *testing.T) {
	t.Parallel()
	assert.False(t, IsCompressed(nil))
	assert.False(t, IsCompressed(&FrameTable{CompressionType: CompressionNone}))
	assert.True(t, IsCompressed(&FrameTable{CompressionType: CompressionLZ4}))
	assert.True(t, IsCompressed(&FrameTable{CompressionType: CompressionZstd}))
}

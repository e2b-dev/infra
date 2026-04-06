package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// threeFrameFT returns a FrameTable with three 1MB uncompressed frames
// and varying compressed sizes, starting at the given offset.
func threeFrameFT(startU, startC int64) *FrameTable {
	ft := &FrameTable{
		compressionType: CompressionLZ4,
		StartAt:         FrameOffset{U: startU, C: startC},
		Frames: []FrameSize{
			{U: 1 << 20, C: 500_000}, // frame 0
			{U: 1 << 20, C: 600_000}, // frame 1
			{U: 1 << 20, C: 400_000}, // frame 2
		},
	}

	return ft
}

func TestFrameFor(t *testing.T) {
	t.Parallel()
	ft := threeFrameFT(0, 0)

	t.Run("first byte of each frame", func(t *testing.T) {
		t.Parallel()
		for i, wantU := range []int64{0, 1 << 20, 2 << 20} {
			start, size, err := ft.FrameFor(wantU)
			require.NoError(t, err, "frame %d", i)
			require.Equal(t, wantU, start.U)
			require.Equal(t, int32(1<<20), size.U)
		}
	})

	t.Run("last byte of frame", func(t *testing.T) {
		t.Parallel()
		start, _, err := ft.FrameFor((1 << 20) - 1)
		require.NoError(t, err)
		require.Equal(t, int64(0), start.U)
	})

	t.Run("returns correct C offset", func(t *testing.T) {
		t.Parallel()
		start, _, err := ft.FrameFor(2 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(1_100_000), start.C) // 500k + 600k
	})

	t.Run("beyond end errors", func(t *testing.T) {
		t.Parallel()
		_, _, err := ft.FrameFor(3 << 20)
		require.Error(t, err)
	})

	t.Run("nil table errors", func(t *testing.T) {
		t.Parallel()
		_, _, err := (*FrameTable)(nil).FrameFor(0)
		require.Error(t, err)
	})

	t.Run("respects StartAt", func(t *testing.T) {
		t.Parallel()
		sub := threeFrameFT(1<<20, 500_000)
		start, _, err := sub.FrameFor(1 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(1<<20), start.U)
		require.Equal(t, int64(500_000), start.C)

		// Before StartAt — no frame should contain offset 0.
		_, _, err = sub.FrameFor(0)
		require.Error(t, err)
	})
}

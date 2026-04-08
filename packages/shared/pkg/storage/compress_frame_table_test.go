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
		Offset:          FrameOffset{U: startU, C: startC},
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

	t.Run("first byte of each frame uncompressed", func(t *testing.T) {
		t.Parallel()
		for i, wantU := range []int64{0, 1 << 20, 2 << 20} {
			r, err := ft.LocateUncompressed(wantU)
			require.NoError(t, err, "frame %d", i)
			require.Equal(t, wantU, r.Offset)
			require.Equal(t, 1<<20, r.Length)
		}
	})

	t.Run("first byte of each frame compressed", func(t *testing.T) {
		t.Parallel()
		wantC := []int64{0, 500_000, 1_100_000}
		wantLen := []int{500_000, 600_000, 400_000}
		for i, offsetU := range []int64{0, 1 << 20, 2 << 20} {
			r, err := ft.LocateCompressed(offsetU)
			require.NoError(t, err, "frame %d", i)
			require.Equal(t, wantC[i], r.Offset, "frame %d C start", i)
			require.Equal(t, wantLen[i], r.Length, "frame %d C length", i)
		}
	})

	t.Run("last byte of frame", func(t *testing.T) {
		t.Parallel()
		r, err := ft.LocateUncompressed((1 << 20) - 1)
		require.NoError(t, err)
		require.Equal(t, int64(0), r.Offset)
	})

	t.Run("returns correct C offset", func(t *testing.T) {
		t.Parallel()
		r, err := ft.LocateCompressed(2 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(1_100_000), r.Offset) // 500k + 600k
	})

	t.Run("beyond end errors", func(t *testing.T) {
		t.Parallel()
		_, err := ft.LocateCompressed(3 << 20)
		require.Error(t, err)
	})

	t.Run("nil table errors", func(t *testing.T) {
		t.Parallel()
		_, err := (*FrameTable)(nil).LocateCompressed(0)
		require.Error(t, err)
	})

	t.Run("respects Offset", func(t *testing.T) {
		t.Parallel()
		sub := threeFrameFT(1<<20, 500_000)

		r, err := sub.LocateUncompressed(1 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(1<<20), r.Offset)

		r, err = sub.LocateCompressed(1 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(500_000), r.Offset)

		// Before Offset — no frame should contain offset 0.
		_, err = sub.LocateUncompressed(0)
		require.Error(t, err)
	})
}

package storage

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// threeFrameFT returns a FrameTable with three 1MB uncompressed frames
// and varying compressed sizes, starting at the given offset.
func threeFrameFT(startU, startC int64) *FrameTable {
	return &FrameTable{
		compressionType: CompressionLZ4,
		entries: []frameEntry{
			{StartU: startU, StartC: startC, SizeU: 1 << 20, SizeC: 500_000},                     // frame 0
			{StartU: startU + 1<<20, StartC: startC + 500_000, SizeU: 1 << 20, SizeC: 600_000},   // frame 1
			{StartU: startU + 2<<20, StartC: startC + 1_100_000, SizeU: 1 << 20, SizeC: 400_000}, // frame 2
		},
	}
}

func TestLocate(t *testing.T) {
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

	t.Run("non-zero start offset", func(t *testing.T) {
		t.Parallel()
		sub := threeFrameFT(1<<20, 500_000)

		r, err := sub.LocateUncompressed(1 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(1<<20), r.Offset)

		r, err = sub.LocateCompressed(1 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(500_000), r.Offset)

		// Before first entry — no frame should contain offset 0.
		_, err = sub.LocateUncompressed(0)
		require.Error(t, err)
	})
}

func TestNewFrameTable(t *testing.T) {
	t.Parallel()

	ft := NewFrameTable(CompressionZstd, []FrameSize{
		{U: 1 << 20, C: 500_000},
		{U: 1 << 20, C: 600_000},
	})

	require.Equal(t, 2, ft.NumFrames())
	require.Equal(t, CompressionZstd, ft.CompressionType())
	require.True(t, ft.IsCompressed())
	require.Equal(t, int64(2<<20), ft.UncompressedSize())
	require.Equal(t, int64(1_100_000), ft.CompressedSize())

	startU, endU, startC, endC := ft.FrameAt(0)
	require.Equal(t, int64(0), startU)
	require.Equal(t, int64(1<<20), endU)
	require.Equal(t, int64(0), startC)
	require.Equal(t, int64(500_000), endC)

	startU, _, startC, _ = ft.FrameAt(1)
	require.Equal(t, int64(1<<20), startU)
	require.Equal(t, int64(500_000), startC)
}

func TestFrameTable_TrimToRanges(t *testing.T) {
	t.Parallel()

	ft := NewFrameTable(CompressionLZ4, []FrameSize{
		{U: 1 << 20, C: 500_000},
		{U: 1 << 20, C: 600_000},
		{U: 1 << 20, C: 400_000},
		{U: 1 << 20, C: 700_000},
	})

	t.Run("all frames retained", func(t *testing.T) {
		t.Parallel()
		trimmed := ft.TrimToRanges([]Range{{Offset: 0, Length: 4 << 20}})
		require.Same(t, ft, trimmed)
	})

	t.Run("single range trims to subset", func(t *testing.T) {
		t.Parallel()
		trimmed := ft.TrimToRanges([]Range{{Offset: 1 << 20, Length: 2 << 20}})
		require.Equal(t, 2, trimmed.NumFrames())

		startU, _, _, _ := trimmed.FrameAt(0)
		require.Equal(t, int64(1<<20), startU)

		startU, _, _, _ = trimmed.FrameAt(1)
		require.Equal(t, int64(2<<20), startU)
	})

	t.Run("two disjoint ranges", func(t *testing.T) {
		t.Parallel()
		trimmed := ft.TrimToRanges([]Range{
			{Offset: 0, Length: 1 << 20},
			{Offset: 3 << 20, Length: 1 << 20},
		})
		require.Equal(t, 2, trimmed.NumFrames())

		startU, _, _, _ := trimmed.FrameAt(0)
		require.Equal(t, int64(0), startU)

		startU, _, _, _ = trimmed.FrameAt(1)
		require.Equal(t, int64(3<<20), startU)
	})

	t.Run("nil table", func(t *testing.T) {
		t.Parallel()
		var nilFT *FrameTable
		require.Nil(t, nilFT.TrimToRanges([]Range{{Offset: 0, Length: 100}}))
	})

	t.Run("sparse lookup works", func(t *testing.T) {
		t.Parallel()
		trimmed := ft.TrimToRanges([]Range{
			{Offset: 0, Length: 1 << 20},
			{Offset: 3 << 20, Length: 1 << 20},
		})

		r, err := trimmed.LocateCompressed(0)
		require.NoError(t, err)
		require.Equal(t, int64(0), r.Offset)

		r, err = trimmed.LocateCompressed(3 << 20)
		require.NoError(t, err)
		require.Equal(t, int64(500_000+600_000+400_000), r.Offset)

		// Gap lookup fails
		_, err = trimmed.LocateCompressed(1 << 20)
		require.Error(t, err)
	})
}

func TestSerializeDeserializeFrameTable(t *testing.T) {
	t.Parallel()

	t.Run("round-trip", func(t *testing.T) {
		t.Parallel()
		ft := NewFrameTable(CompressionZstd, []FrameSize{
			{U: 2048, C: 1024},
			{U: 4096, C: 3500},
		})

		var buf bytes.Buffer
		require.NoError(t, ft.Serialize(&buf))

		got, err := DeserializeFrameTable(&buf)
		require.NoError(t, err)
		require.Equal(t, ft.NumFrames(), got.NumFrames())
		require.Equal(t, ft.CompressionType(), got.CompressionType())

		for i := range ft.NumFrames() {
			wSU, wEU, wSC, wEC := ft.FrameAt(i)
			gSU, gEU, gSC, gEC := got.FrameAt(i)
			require.Equal(t, wSU, gSU, "frame %d StartU", i)
			require.Equal(t, wEU, gEU, "frame %d EndU", i)
			require.Equal(t, wSC, gSC, "frame %d StartC", i)
			require.Equal(t, wEC, gEC, "frame %d EndC", i)
		}
	})

	t.Run("nil writes zeros", func(t *testing.T) {
		t.Parallel()
		var buf bytes.Buffer
		require.NoError(t, (*FrameTable)(nil).Serialize(&buf))

		got, err := DeserializeFrameTable(&buf)
		require.NoError(t, err)
		require.Nil(t, got)
	})
}

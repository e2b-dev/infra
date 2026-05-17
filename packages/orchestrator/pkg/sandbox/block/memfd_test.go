//go:build linux

package block

import (
	"context"
	"crypto/rand"
	"syscall"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func newTestMemfd(t *testing.T, size int64) (fd int, data []byte) {
	t.Helper()

	fd, err := unix.MemfdCreate("test", 0)
	require.NoError(t, err)
	require.NoError(t, unix.Ftruncate(fd, size))

	data = make([]byte, size)
	_, err = rand.Read(data)
	require.NoError(t, err)

	_, err = syscall.Pwrite(fd, data, 0)
	require.NoError(t, err)

	return fd, data
}

func TestMemfd_SliceOutOfBounds(t *testing.T) {
	t.Parallel()

	const size = 4 * header.PageSize
	fd, _ := newTestMemfd(t, size)

	m := NewFromFd(fd, size)
	t.Cleanup(func() { _ = m.Close() })

	cases := []struct {
		name     string
		off, len int64
	}{
		{"negative offset", -1, header.PageSize},
		{"offset past size", size + 1, header.PageSize},
		{"length spills past end", size - header.PageSize + 1, header.PageSize},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := m.Slice(tc.off, tc.len)
			require.Error(t, err)
		})
	}
}

func TestMemfdCache_FullRange(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 30

	fd, expected := newTestMemfd(t, size)
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), []Range{{Start: 0, Size: size}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, size)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(size), n)
	require.Equal(t, expected, got)
}

func TestMemfdCache_MultipleRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 6

	fd, expected := newTestMemfd(t, size)
	ranges := []Range{
		{Start: 0, Size: pageSize},
		{Start: pageSize * 2, Size: pageSize},
		{Start: pageSize * 5, Size: pageSize},
	}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cases := []struct{ cacheOffset, srcOffset int64 }{
		{0, 0},
		{pageSize, pageSize * 2},
		{pageSize * 2, pageSize * 5},
	}
	for _, tc := range cases {
		got := make([]byte, pageSize)
		n, err := cache.ReadAt(got, tc.cacheOffset)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.Equal(t, expected[tc.srcOffset:tc.srcOffset+pageSize], got)
	}
}

// Regression: copyFromMemfd used to index src[srcOff:...] with srcOff in
// guest-absolute space, panicking when Range.Start > 0.
func TestMemfdCache_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 8

	fd, expected := newTestMemfd(t, size)
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), []Range{{Start: pageSize * 3, Size: pageSize * 2}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, pageSize*2)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(pageSize*2), n)
	require.Equal(t, expected[pageSize*3:pageSize*5], got)
}

func TestMemfdCache_RangeLargerThanCopyChunk(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	rangeSize := memfdCopyChunkSize*2 + blockSize
	size := rangeSize + blockSize

	fd, expected := newTestMemfd(t, size)
	cache, err := NewCacheFromMemfd(t.Context(), blockSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), []Range{{Start: 0, Size: rangeSize}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, rangeSize)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(rangeSize), n)
	require.Equal(t, expected[:rangeSize], got)
}

// Mirrors the runtime exportMemory flow: BitsetRanges merges adjacent dirty
// pages into one Range; non-adjacent ones stay separate.
func TestMemfdCache_DirtyBitmap(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 8

	fd, expected := newTestMemfd(t, size)
	dirty := roaring.New()
	dirty.Add(1)
	dirty.Add(2)
	dirty.Add(6)

	var ranges []Range
	for r := range BitsetRanges(dirty, pageSize) {
		ranges = append(ranges, r)
	}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cases := []struct{ cacheOffset, srcOffset, length int64 }{
		{0, pageSize, pageSize * 2},
		{pageSize * 2, pageSize * 6, pageSize},
	}
	for _, tc := range cases {
		got := make([]byte, tc.length)
		n, err := cache.ReadAt(got, tc.cacheOffset)
		require.NoError(t, err)
		require.Equal(t, int(tc.length), n)
		require.Equal(t, expected[tc.srcOffset:tc.srcOffset+tc.length], got)
	}
}

func TestMemfdCache_ContextCancellation(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 16

	fd, _ := newTestMemfd(t, size)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := NewCacheFromMemfd(ctx, pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), []Range{{Start: 0, Size: size}})
	require.ErrorIs(t, err, context.Canceled)
}

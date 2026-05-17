//go:build linux

package block

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func newTestMemfd(t *testing.T, size int64) (memfd *Memfd, data []byte) {
	t.Helper()

	fd, err := unix.MemfdCreate("test", 0)
	require.NoError(t, err)
	require.NoError(t, unix.Ftruncate(fd, size))

	data = make([]byte, size)
	_, err = rand.Read(data)
	require.NoError(t, err)

	_, err = unix.Pwrite(fd, data, 0)
	require.NoError(t, err)

	memfd, err = NewFromFd(fd)
	require.NoError(t, err)

	return memfd, data
}

func TestMemfd_SliceOutOfBounds(t *testing.T) {
	t.Parallel()

	const size = 4 * header.PageSize
	m, _ := newTestMemfd(t, size)
	t.Cleanup(func() { _ = m.Close() })

	_, err := m.Slice(size-header.PageSize+1, header.PageSize)
	require.Error(t, err)
}

func TestMemfdCache_FullRange(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 30

	memfd, expected := newTestMemfd(t, size)
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, []Range{{Start: 0, Size: size}})
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

	memfd, expected := newTestMemfd(t, size)
	ranges := []Range{
		{Start: 0, Size: pageSize},
		{Start: pageSize * 2, Size: pageSize},
		{Start: pageSize * 5, Size: pageSize},
	}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, ranges)
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

	memfd, expected := newTestMemfd(t, size)
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, []Range{{Start: pageSize * 3, Size: pageSize * 2}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, pageSize*2)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(pageSize*2), n)
	require.Equal(t, expected[pageSize*3:pageSize*5], got)
}

func TestMemfdCache_ContextCancellation(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 16

	memfd, _ := newTestMemfd(t, size)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := NewCacheFromMemfd(ctx, pageSize, t.TempDir()+"/cache", memfd, []Range{{Start: 0, Size: size}})
	require.ErrorIs(t, err, context.Canceled)
}

//go:build linux

package block

import (
	"context"
	"crypto/rand"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
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

// dirtyBitmap returns a bitmap with the given block indices set.
func dirtyBitmap(blocks ...uint32) *roaring.Bitmap {
	b := roaring.New()
	b.AddMany(blocks)

	return b
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
	numPages := uint32(30)

	memfd, expected := newTestMemfd(t, pageSize*int64(numPages))
	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, len(expected))
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, len(expected), n)
	require.Equal(t, expected, got)
}

func TestMemfdCache_MultipleRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*6)

	// Pages 0, 2, 5 — non-adjacent so BitsetRanges emits three ranges; cache
	// packs them in iteration order.
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirtyBitmap(0, 2, 5))
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
// guest-absolute space, panicking when the first Range.Start was > 0.
func TestMemfdCache_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*8)

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirtyBitmap(3, 4))
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
	numPages := uint32(16)
	memfd, _ := newTestMemfd(t, pageSize*int64(numPages))

	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := NewCacheFromMemfd(ctx, pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.ErrorIs(t, err, context.Canceled)
}

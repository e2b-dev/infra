//go:build linux

package block

import (
	"context"
	"crypto/rand"
	"os"
	"syscall"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// newTestMemfd creates an anonymous memfd of `size` bytes, fills it with
// random data, and returns the fd plus the data that was written.
// The caller is responsible for closing the fd (typically by handing it to
// NewFromFd / NewCacheFromMemfd, which closes it).
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

// --- Memfd ---------------------------------------------------------------

func TestMemfd_SliceFullRange(t *testing.T) {
	t.Parallel()

	const size = 4 * header.PageSize
	fd, data := newTestMemfd(t, size)

	m := NewFromFd(fd, size)
	t.Cleanup(func() { _ = m.Close() })

	got, err := m.Slice(0, size)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestMemfd_SliceMultipleViewsShareMmap(t *testing.T) {
	t.Parallel()

	// ensureMapped uses sync.Once; repeated Slice calls must reuse the same
	// mapping and return mutually consistent views of the same bytes.
	const size = 4 * header.PageSize
	fd, data := newTestMemfd(t, size)

	m := NewFromFd(fd, size)
	t.Cleanup(func() { _ = m.Close() })

	first, err := m.Slice(0, header.PageSize)
	require.NoError(t, err)
	require.Equal(t, data[:header.PageSize], first)

	// Slice the tail. Must also succeed (mmap reused, no second mmap call).
	second, err := m.Slice(header.PageSize, 3*header.PageSize)
	require.NoError(t, err)
	require.Equal(t, data[header.PageSize:], second)

	// Overlapping views point at the same backing memory.
	third, err := m.Slice(0, size)
	require.NoError(t, err)
	require.Equal(t, &first[0], &third[0], "overlapping Slice views must share backing memory")
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
		{"offset at size", size, header.PageSize},
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

func TestMemfd_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	const size = 2 * header.PageSize
	fd, _ := newTestMemfd(t, size)

	m := NewFromFd(fd, size)

	// Force the lazy mmap so Close has both mmap + fd to release.
	_, err := m.Slice(0, header.PageSize)
	require.NoError(t, err)

	require.NoError(t, m.Close())
	// Second close must not panic and must not return an error for
	// already-released resources — both fields are nil/-1 by now.
	require.NoError(t, m.Close())
}

func TestMemfd_CloseWithoutMmap(t *testing.T) {
	t.Parallel()

	// If Slice is never called, Close must still close the fd — and not
	// attempt Munmap on a nil mapping.
	const size = 2 * header.PageSize
	fd, _ := newTestMemfd(t, size)

	m := NewFromFd(fd, size)
	require.NoError(t, m.Close())
}

// --- MemfdCache ----------------------------------------------------------

func TestMemfdCache_FullRange(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 30

	fd, expected := newTestMemfd(t, size)

	ranges := []Range{{Start: 0, Size: size}}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
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
	numPages := int64(6)
	size := pageSize * numPages

	fd, expected := newTestMemfd(t, size)

	// Pages 0, 2, 5 — non-contiguous, packs the cache in iteration order.
	ranges := []Range{
		{Start: 0, Size: pageSize},
		{Start: pageSize * 2, Size: pageSize},
		{Start: pageSize * 5, Size: pageSize},
	}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cases := []struct {
		cacheOffset int64
		srcOffset   int64
	}{
		{0, 0},
		{pageSize, pageSize * 2},
		{pageSize * 2, pageSize * 5},
	}
	for _, tc := range cases {
		got := make([]byte, pageSize)
		n, err := cache.ReadAt(got, tc.cacheOffset)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.Equal(t, expected[tc.srcOffset:tc.srcOffset+pageSize], got,
			"page at cache offset %d", tc.cacheOffset)
	}
}

// Regression: writeToDisk used to index src[srcOff:...] with srcOff in
// guest-absolute space, which panicked the first time a Range.Start was > 0.
// This test would have caught it.
func TestMemfdCache_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := int64(8)
	size := pageSize * numPages

	fd, expected := newTestMemfd(t, size)

	// Skip the first three pages entirely; the only Range starts at
	// pageSize*3 — this is the case that used to panic.
	ranges := []Range{{Start: pageSize * 3, Size: pageSize * 2}}

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, pageSize*2)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(pageSize*2), n)
	require.Equal(t, expected[pageSize*3:pageSize*5], got)
}

// Range.Size > blockSize forces writeToDisk's inner chunked copy loop to
// iterate more than once. Verifies correctness across chunk boundaries.
func TestMemfdCache_RangeLargerThanBlockSize(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize) // 4 KiB
	rangeSize := blockSize * 5          // 5 chunks per Range
	size := rangeSize + blockSize       // memfd a bit larger than the Range
	fd, expected := newTestMemfd(t, size)

	ranges := []Range{{Start: 0, Size: rangeSize}}

	cache, err := NewCacheFromMemfd(t.Context(), blockSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, rangeSize)
	n, err := cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, int(rangeSize), n)
	require.Equal(t, expected[:rangeSize], got)
}

// Exercises the BitsetRanges-derived merged-range path: pages 1,2 merge
// into one Range, page 6 is separate. Mirrors the runtime exportMemory flow.
func TestMemfdCache_DirtyBitmap(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := int64(8)
	size := pageSize * numPages

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

	cases := []struct {
		cacheOffset, srcOffset, length int64
	}{
		{0, pageSize, pageSize * 2}, // merged 1–2
		{pageSize * 2, pageSize * 6, pageSize},
	}
	for _, tc := range cases {
		got := make([]byte, tc.length)
		n, err := cache.ReadAt(got, tc.cacheOffset)
		require.NoError(t, err)
		require.Equal(t, int(tc.length), n)
		require.Equal(t, expected[tc.srcOffset:tc.srcOffset+tc.length], got,
			"chunk at cache offset %d", tc.cacheOffset)
	}
}

func TestMemfdCache_EmptyRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	fd, _ := newTestMemfd(t, pageSize*4)

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(pageSize*4)), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	sz, err := cache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, sz)
}

func TestMemfdCache_ContextCancellation(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 16
	fd, _ := newTestMemfd(t, size)

	ranges := []Range{{Start: 0, Size: size}}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := NewCacheFromMemfd(ctx, pageSize, t.TempDir()+"/cache", NewFromFd(fd, int(size)), ranges)
	require.ErrorIs(t, err, context.Canceled)
}

// On the happy path, NewCacheFromMemfd closes the memfd internally and nils
// the field, so subsequent MemfdCache.Close must still cleanly close the
// underlying *Cache without trying to re-close the memfd. The cache file is
// then removed by Cache.Close.
func TestMemfdCache_CloseAfterSuccessfulPopulationRemovesCacheFile(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	size := pageSize * 4
	fd, _ := newTestMemfd(t, size)

	cachePath := t.TempDir() + "/cache"
	cache, err := NewCacheFromMemfd(t.Context(), pageSize, cachePath, NewFromFd(fd, int(size)), []Range{{Start: 0, Size: size}})
	require.NoError(t, err)

	// File exists while the cache is alive.
	_, err = os.Stat(cachePath)
	require.NoError(t, err)

	require.NoError(t, cache.Close())

	// Cache.Close removes the backing file.
	_, err = os.Stat(cachePath)
	require.ErrorIs(t, err, os.ErrNotExist, "expected cache file to be removed, got: %v", err)
}

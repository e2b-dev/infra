//go:build linux

package block

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

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

func TestNewCacheFromMemfd_NonAdjacentBlocks(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*6)

	// Non-adjacent blocks: BitsetRanges emits three separate ranges; the
	// cache packs them in iteration order.
	dirty := roaring.New()
	dirty.AddMany([]uint32{0, 2, 5})

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	for i, srcBlock := range []int64{0, 2, 5} {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, int64(i)*pageSize)
		require.NoError(t, err)
		require.Equal(t, expected[srcBlock*pageSize:(srcBlock+1)*pageSize], got)
	}
}

// Regression: the copy loop used to index src[srcOff:...] with srcOff in
// guest-absolute space, panicking when the first range started past zero.
func TestNewCacheFromMemfd_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*8)

	dirty := roaring.New()
	dirty.AddMany([]uint32{3, 4})

	cache, err := NewCacheFromMemfd(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	got := make([]byte, pageSize*2)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, expected[pageSize*3:pageSize*5], got)
}

// In-flight reads must return memfd data even before runCopy reaches the
// range, so a sandbox can resume from a just-paused snapshot without waiting.
func TestNewCacheFromMemfdAsync_InFlightReadAt(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*8)
	dirty := roaring.New()
	dirty.AddMany([]uint32{1, 3, 5})

	cache, err := NewCacheFromMemfdAsync(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	for i, srcBlock := range []int64{1, 3, 5} {
		got := make([]byte, pageSize)
		_, err := cache.ReadAt(got, int64(i)*pageSize)
		require.NoError(t, err)
		require.Equal(t, expected[srcBlock*pageSize:(srcBlock+1)*pageSize], got)
	}

	require.NoError(t, cache.Wait(t.Context()))
}

// Slice returned during a copy must outlive the memfd: runCopy demand-pages
// any not-yet-copied range into the cache file, then closes memfd. The slice
// points at the cache mmap, so it remains valid until Cache.Close.
func TestNewCacheFromMemfdAsync_SliceOutlivesMemfd(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize*4)
	dirty := roaring.New()
	dirty.AddMany([]uint32{0, 1, 2, 3})

	cache, err := NewCacheFromMemfdAsync(t.Context(), pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	s, err := cache.Slice(pageSize, pageSize*2)
	require.NoError(t, err)
	want := make([]byte, pageSize*2)
	copy(want, expected[pageSize:pageSize*3])

	require.NoError(t, cache.Wait(t.Context()))
	require.Equal(t, want, s)
}

// Detached background copy: cancelling the parent context must not abort
// the goroutine since the caller (sandbox.go) hands ownership to MemfdCache.
func TestNewCacheFromMemfdAsync_DetachesFromParent(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	memfd, expected := newTestMemfd(t, pageSize)
	dirty := roaring.New()
	dirty.Add(0)

	parent, cancel := context.WithCancel(t.Context())
	cache, err := NewCacheFromMemfdAsync(parent, pageSize, t.TempDir()+"/cache", memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cancel()

	waitCtx, cancelWait := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancelWait()
	require.NoError(t, cache.Wait(waitCtx))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, expected, got)
}

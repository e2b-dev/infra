//go:build linux

package block

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
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

// fullDirty returns a bitmap marking every block in [0, size/blockSize) dirty.
func fullDirty(size, blockSize int64) *roaring.Bitmap {
	b := roaring.New()
	b.AddRange(0, uint64(size/blockSize))

	return b
}

<<<<<<< HEAD
func TestNewCacheFromMemfd_NonAdjacentBlocks(t *testing.T) {
=======
// fullDirty returns a bitmap marking every block in [0, size/blockSize) dirty.
func fullDirty(size, blockSize int64) *roaring.Bitmap {
	b := roaring.New()
	b.AddRange(0, uint64(size/blockSize))

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
>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
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

// --- NewCacheFromMemfdDeduped --------------------------------------------

// newTestMemfdWith creates a memfd populated with the given bytes. Unlike
// newTestMemfd it lets the caller dictate exact contents — needed for dedup
// tests that arrange specific match/differ patterns against a base.
func newTestMemfdWith(t *testing.T, data []byte) *Memfd {
	t.Helper()

	fd, err := unix.MemfdCreate("test", 0)
	require.NoError(t, err)
	require.NoError(t, unix.Ftruncate(fd, int64(len(data))))

	if len(data) > 0 {
		_, err = unix.Pwrite(fd, data, 0)
		require.NoError(t, err)
	}

	memfd, err := NewFromFd(fd)
	require.NoError(t, err)

	return memfd
}

// fakeOriginalDevice satisfies ReadonlyDevice over a fixed byte buffer.
// Only ReadAt is exercised by NewCacheFromMemfdDeduped; the rest are stubs.
type fakeOriginalDevice struct {
	data []byte
}

func (f *fakeOriginalDevice) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func (f *fakeOriginalDevice) Size(context.Context) (int64, error)                 { return int64(len(f.data)), nil }
func (f *fakeOriginalDevice) Close() error                                        { return nil }
func (f *fakeOriginalDevice) Slice(context.Context, int64, int64) ([]byte, error) { return nil, nil }
func (f *fakeOriginalDevice) BlockSize() int64                                    { return int64(header.PageSize) }
func (f *fakeOriginalDevice) Header() *header.Header                              { return nil }
func (f *fakeOriginalDevice) SwapHeader(*header.Header)                           {}

// erroringOriginalDevice returns sentinel from every ReadAt. Used to verify
// that NewCacheFromMemfdDeduped propagates base-read failures.
type erroringOriginalDevice struct {
	fakeOriginalDevice

	err error
}

func (e *erroringOriginalDevice) ReadAt(context.Context, []byte, int64) (int, error) {
	return 0, e.err
}

func TestNewCacheFromMemfdDeduped_AllPagesMatch(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2 // 8 pages

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	memfd := newTestMemfdWith(t, data) // memfd content == base content

<<<<<<< HEAD
=======
	dirty := roaring.New(); dirty.AddRange(0, uint64(size/blockSize))

>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: data},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
<<<<<<< HEAD
		fullDirty(size, blockSize),
=======
		dirty,
>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	sz, err := cache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, sz, "no pages differ — dedup cache must be empty")

	require.EqualValues(t, 0, meta.Dirty.GetCardinality())
	require.EqualValues(t, 0, meta.Empty.GetCardinality())
	require.EqualValues(t, header.PageSize, meta.BlockSize)
}

func TestNewCacheFromMemfdDeduped_AllPagesDiffer(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2 // 8 pages

	srcData := make([]byte, size)
	_, err := rand.Read(srcData)
	require.NoError(t, err)
	origData := make([]byte, size) // all zeros — every page differs

	memfd := newTestMemfdWith(t, srcData)

<<<<<<< HEAD
=======
	dirty := roaring.New(); dirty.AddRange(0, uint64(size/blockSize))

>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
<<<<<<< HEAD
		fullDirty(size, blockSize),
=======
		dirty,
>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	numPages := size / pageSize
	require.EqualValues(t, numPages, meta.Dirty.GetCardinality())
	require.EqualValues(t, header.PageSize, meta.BlockSize)

	// Every page is packed at index i × pageSize and equals srcData's page i.
	for i := range numPages {
		got := make([]byte, pageSize)
		n, err := cache.ReadAt(got, i*pageSize)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.Equal(t, srcData[i*pageSize:(i+1)*pageSize], got, "page %d", i)
	}
}

func TestNewCacheFromMemfdDeduped_MixedPages(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 3 // 12 pages across 3 blocks

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	// Differ pages 2 and 7.
	differingPage2 := bytes.Repeat([]byte{0xAA}, int(pageSize))
	differingPage7 := bytes.Repeat([]byte{0xBB}, int(pageSize))
	copy(srcData[2*pageSize:], differingPage2)
	copy(srcData[7*pageSize:], differingPage7)

	memfd := newTestMemfdWith(t, srcData)

<<<<<<< HEAD
=======
	dirty := roaring.New(); dirty.AddRange(0, uint64(size/blockSize))

>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
<<<<<<< HEAD
		fullDirty(size, blockSize),
=======
		dirty,
>>>>>>> e4d69ade1 (feat(cache): wire deduplication logic)
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 2, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(2))
	require.True(t, meta.Dirty.Contains(7))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differingPage2, got, "first packed page is the differing page at idx 2")

	_, err = cache.ReadAt(got, pageSize)
	require.NoError(t, err)
	require.Equal(t, differingPage7, got, "second packed page is the differing page at idx 7")
}

// Regression: dedupRange used to index src[srcOff:...] with srcOff in
// guest-absolute space, which panicked on any Range.Start > 0.
func TestNewCacheFromMemfdDeduped_NonZeroRangeStart(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 3 // 12 pages

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	// Differ one page well past the start of the memfd.
	differing := bytes.Repeat([]byte{0xCC}, int(pageSize))
	copy(srcData[9*pageSize:], differing)

	memfd := newTestMemfdWith(t, srcData)

	// Bitmap covers only the third block; Start is non-zero.
	dirty := roaring.New()
	dirty.Add(2)

	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		dirty,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 1, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(9))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differing, got)
}

// Two non-contiguous Ranges → verifies cacheOff advances correctly across
// independent dedupRange calls, and packed output preserves iteration order.
func TestNewCacheFromMemfdDeduped_MultipleRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 4 // 16 pages

	origData := make([]byte, size)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, size)
	copy(srcData, origData)

	// Differ page 1 (inside first range) and page 13 (inside last range).
	p1 := bytes.Repeat([]byte{0xD1}, int(pageSize))
	p13 := bytes.Repeat([]byte{0xD2}, int(pageSize))
	copy(srcData[1*pageSize:], p1)
	copy(srcData[13*pageSize:], p13)

	memfd := newTestMemfdWith(t, srcData)

	// Two non-contiguous blocks: 0 and 3.
	dirty := roaring.New()
	dirty.Add(0)
	dirty.Add(3)

	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		dirty,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 2, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(1))
	require.True(t, meta.Dirty.Contains(13))

	got := make([]byte, pageSize)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, p1, got)

	_, err = cache.ReadAt(got, pageSize)
	require.NoError(t, err)
	require.Equal(t, p13, got)
}

func TestNewCacheFromMemfdDeduped_EmptyRanges(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	data := make([]byte, size)
	memfd := newTestMemfdWith(t, data)

	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: data},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		roaring.New(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	sz, err := cache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, sz)
	require.EqualValues(t, 0, meta.Dirty.GetCardinality())
	require.EqualValues(t, 0, meta.Empty.GetCardinality())
}

func TestNewCacheFromMemfdDeduped_ContextCancellation(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 4

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	memfd := newTestMemfdWith(t, data)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err = NewCacheFromMemfdDeduped(
		ctx,
		&fakeOriginalDevice{data: data},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		fullDirty(size, blockSize),
	)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNewCacheFromMemfdDeduped_OriginalMemfileReadError(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	data := make([]byte, size)
	_, err := rand.Read(data)
	require.NoError(t, err)

	memfd := newTestMemfdWith(t, data)

	sentinel := errors.New("base read failed")
	_, _, err = NewCacheFromMemfdDeduped(
		t.Context(),
		&erroringOriginalDevice{
			fakeOriginalDevice: fakeOriginalDevice{data: data},
			err:                sentinel,
		},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		fullDirty(size, blockSize),
	)
	require.ErrorIs(t, err, sentinel)
}

// On the happy path NewCacheFromMemfdDeduped closes the memfd internally.
// MemfdCache.Close must still cleanly close the inner *Cache (which removes
// the on-disk file).
func TestNewCacheFromMemfdDeduped_CloseRemovesCacheFile(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize

	origData := make([]byte, size)
	srcData := make([]byte, size)
	_, err := rand.Read(srcData)
	require.NoError(t, err)

	memfd := newTestMemfdWith(t, srcData)

	cachePath := t.TempDir() + "/dedup"
	cache, _, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		cachePath,
		memfd,
		fullDirty(size, blockSize),
	)
	require.NoError(t, err)

	_, err = os.Stat(cachePath)
	require.NoError(t, err, "cache file should exist while MemfdCache is alive")

	require.NoError(t, cache.Close())

	_, err = os.Stat(cachePath)
	require.ErrorIs(t, err, os.ErrNotExist, "cache file should be removed after Close")
}

// Pages that match the base and happen to be all-zero must be recorded in
// Empty (so the merged header maps them to uuid.Nil → zero-fill at read),
// rather than relying on a fall-through to the parent's diff — which for
// the synthetic Empty template has no real backing file and would error.
//
// Non-zero pages that match the base must NOT land in Empty (those rely on
// the merged mapping keeping the parent's mapping pointing at the real
// parent diff).
func TestNewCacheFromMemfdDeduped_ZeroMatchingPagesGoIntoEmpty(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	blockSize := 4 * pageSize
	size := blockSize * 2 // 8 pages

	// Base: pages 0..3 zero, pages 4..7 random non-zero.
	origData := make([]byte, size)
	_, err := rand.Read(origData[4*pageSize:])
	require.NoError(t, err)

	// Memfd matches base exactly — no Dirty pages, but the first half
	// matches a *zero* base and the second half matches a *non-zero* base.
	srcData := make([]byte, size)
	copy(srcData, origData)

	memfd := newTestMemfdWith(t, srcData)

	cache, meta, err := NewCacheFromMemfdDeduped(
		t.Context(),
		&fakeOriginalDevice{data: origData},
		blockSize,
		t.TempDir()+"/dedup",
		memfd,
		fullDirty(size, blockSize),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	require.EqualValues(t, 0, meta.Dirty.GetCardinality(), "no pages differ from base")

	// Only the zero pages (0..3) should be in Empty.
	require.EqualValues(t, 4, meta.Empty.GetCardinality())
	for i := range uint32(4) {
		require.True(t, meta.Empty.Contains(i), "zero-matching page %d should be in Empty", i)
	}
	for i := uint32(4); i < 8; i++ {
		require.False(t, meta.Empty.Contains(i), "non-zero-matching page %d should not be in Empty", i)
	}
}

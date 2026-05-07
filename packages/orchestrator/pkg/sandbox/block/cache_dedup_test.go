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

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// fakeOriginalDevice satisfies ReadonlyDevice over a fixed byte buffer.
// Only ReadAt is exercised by Cache.Dedup; other methods are stubs.
type fakeOriginalDevice struct {
	data      []byte
	blockSize int64
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
func (f *fakeOriginalDevice) BlockSize() int64                                    { return f.blockSize }
func (f *fakeOriginalDevice) Header() *header.Header                              { return nil }
func (f *fakeOriginalDevice) SwapHeader(*header.Header)                           {}

// buildPackedSrcCache builds the packed-by-block source Cache that Cache.Dedup
// expects: bytes from the dirty positions of `original`, concatenated in
// BitsetRanges iteration order, with the cache's blockSize set to `blockSize`.
func buildPackedSrcCache(t *testing.T, original []byte, dirty *roaring.Bitmap, blockSize int64) *Cache {
	t.Helper()

	var packed []byte
	for r := range BitsetRanges(dirty, blockSize) {
		packed = append(packed, original[r.Start:r.Start+r.Size]...)
	}

	cache, err := NewCache(int64(len(packed)), blockSize, t.TempDir()+"/src-cache", false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	if len(packed) > 0 {
		_, err = cache.WriteAt(packed, 0)
		require.NoError(t, err)
	}
	return cache
}

func allBlocksDirty(numBlocks uint32) *roaring.Bitmap {
	b := roaring.New()
	for i := uint32(0); i < numBlocks; i++ {
		b.Add(i)
	}
	return b
}

func TestCacheDedup_AllPagesUnchanged(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize) // 16 KiB
		totalSize = blockSize * 4
	)

	data := make([]byte, totalSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: data, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	cacheSize, err := dedupCache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, cacheSize, "no pages differ from base — dedup cache must be empty")

	require.EqualValues(t, 0, diffMeta.Dirty.GetCardinality())
	require.EqualValues(t, totalSize/header.PageSize, diffMeta.Empty.GetCardinality(),
		"every page must be classified as Empty when nothing differs")
	require.EqualValues(t, header.PageSize, diffMeta.BlockSize)
}

func TestCacheDedup_AllPagesDiffer(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 2
	)

	srcData := make([]byte, totalSize)
	_, err := rand.Read(srcData)
	require.NoError(t, err)
	origData := make([]byte, totalSize) // all zeros — every page differs

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	numPages := totalSize / pageSize
	require.EqualValues(t, numPages, diffMeta.Dirty.GetCardinality())
	require.EqualValues(t, 0, diffMeta.Empty.GetCardinality())
	require.EqualValues(t, header.PageSize, diffMeta.BlockSize)

	// Every page i must be at packed offset i*pageSize and equal srcData's page i.
	for i := int64(0); i < numPages; i++ {
		got := make([]byte, pageSize)
		n, err := dedupCache.ReadAt(got, i*pageSize)
		require.NoError(t, err)
		require.Equal(t, int(pageSize), n)
		require.Equal(t, srcData[i*pageSize:(i+1)*pageSize], got, "page %d", i)
	}
}

func TestCacheDedup_MixedPages(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 3 // 12 pages across 3 blocks
	)

	origData := make([]byte, totalSize)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, totalSize)
	copy(srcData, origData)

	differingPage2 := bytes.Repeat([]byte{0xAA}, int(pageSize))
	differingPage7 := bytes.Repeat([]byte{0xBB}, int(pageSize))
	copy(srcData[2*pageSize:], differingPage2)
	copy(srcData[7*pageSize:], differingPage7)

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	require.EqualValues(t, 2, diffMeta.Dirty.GetCardinality())
	require.True(t, diffMeta.Dirty.Contains(2))
	require.True(t, diffMeta.Dirty.Contains(7))
	require.EqualValues(t, totalSize/pageSize-2, diffMeta.Empty.GetCardinality())

	got := make([]byte, pageSize)
	_, err = dedupCache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differingPage2, got)

	_, err = dedupCache.ReadAt(got, pageSize)
	require.NoError(t, err)
	require.Equal(t, differingPage7, got)
}

// Regression test for merged-range handling. BitsetRanges merges contiguous bits
// into one Range, so the dedup loop must walk r.Size, not blockSize.
func TestCacheDedup_ContiguousDirtyBlocks(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 5 // 20 pages
	)

	origData := make([]byte, totalSize)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, totalSize)
	copy(srcData, origData)

	// Modify one page in each of three contiguous dirty blocks.
	differingPage4 := bytes.Repeat([]byte{0xA1}, int(pageSize))
	differingPage9 := bytes.Repeat([]byte{0xA2}, int(pageSize))
	differingPage14 := bytes.Repeat([]byte{0xA3}, int(pageSize))
	copy(srcData[4*pageSize:], differingPage4)   // block 1
	copy(srcData[9*pageSize:], differingPage9)   // block 2
	copy(srcData[14*pageSize:], differingPage14) // block 3

	// Dirty blocks 1, 2, 3 — BitsetRanges merges them into a single 3-block range.
	dirty := roaring.New()
	dirty.Add(1)
	dirty.Add(2)
	dirty.Add(3)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	require.EqualValues(t, 3, diffMeta.Dirty.GetCardinality())
	require.True(t, diffMeta.Dirty.Contains(4))
	require.True(t, diffMeta.Dirty.Contains(9))
	require.True(t, diffMeta.Dirty.Contains(14))
	require.EqualValues(t, totalSize/pageSize-3, diffMeta.Empty.GetCardinality(),
		"every non-Dirty page (including those in non-dirty blocks) must be Empty")

	cases := []struct {
		offset int64
		want   []byte
	}{
		{0, differingPage4},
		{pageSize, differingPage9},
		{2 * pageSize, differingPage14},
	}
	for _, tc := range cases {
		got := make([]byte, pageSize)
		_, err := dedupCache.ReadAt(got, tc.offset)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "page at offset %d", tc.offset)
	}
}

// Regression test for cacheOffset advancement across multiple separate ranges.
// Forces three independent BitsetRanges entries.
func TestCacheDedup_NonContiguousDirtyBlocks(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 6 // 24 pages
	)

	origData := make([]byte, totalSize)
	_, err := rand.Read(origData)
	require.NoError(t, err)

	srcData := make([]byte, totalSize)
	copy(srcData, origData)

	p1 := bytes.Repeat([]byte{0xC1}, int(pageSize))
	p13 := bytes.Repeat([]byte{0xC2}, int(pageSize))
	p21 := bytes.Repeat([]byte{0xC3}, int(pageSize))
	copy(srcData[1*pageSize:], p1)   // block 0
	copy(srcData[13*pageSize:], p13) // block 3
	copy(srcData[21*pageSize:], p21) // block 5

	dirty := roaring.New()
	dirty.Add(0)
	dirty.Add(3)
	dirty.Add(5)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	require.EqualValues(t, 3, diffMeta.Dirty.GetCardinality())
	require.True(t, diffMeta.Dirty.Contains(1))
	require.True(t, diffMeta.Dirty.Contains(13))
	require.True(t, diffMeta.Dirty.Contains(21))
	require.EqualValues(t, totalSize/pageSize-3, diffMeta.Empty.GetCardinality(),
		"every non-Dirty page (including those in non-dirty blocks) must be Empty")

	cases := []struct {
		offset int64
		want   []byte
	}{
		{0, p1},
		{pageSize, p13},
		{2 * pageSize, p21},
	}
	for _, tc := range cases {
		got := make([]byte, pageSize)
		_, err := dedupCache.ReadAt(got, tc.offset)
		require.NoError(t, err)
		require.Equal(t, tc.want, got, "page at offset %d", tc.offset)
	}
}

func TestCacheDedup_EmptyDirtyBitmap(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		totalSize = blockSize * 4
	)

	data := make([]byte, totalSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := roaring.New()
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: data, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	cacheSize, err := dedupCache.Size()
	require.NoError(t, err)
	require.EqualValues(t, 0, cacheSize)
	require.EqualValues(t, 0, diffMeta.Dirty.GetCardinality())
	require.EqualValues(t, totalSize/header.PageSize, diffMeta.Empty.GetCardinality())
}

// Pages that differ from base but are themselves all-zero should still be
// reported as Dirty (they differ) and read back as zeros from the dedup cache.
func TestCacheDedup_ZeroPageWithDifferingBase(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 2
	)

	origData := bytes.Repeat([]byte{0xFF}, int(totalSize))
	srcData := make([]byte, totalSize)
	copy(srcData, origData)

	zeroPage := make([]byte, pageSize)
	copy(srcData[3*pageSize:], zeroPage) // page 3 is now all-zero

	dirty := roaring.New()
	dirty.Add(0)
	dirty.Add(1)

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)

	dedupCache, diffMeta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedupCache.Close() })

	require.EqualValues(t, 1, diffMeta.Dirty.GetCardinality())
	require.True(t, diffMeta.Dirty.Contains(3))
	require.EqualValues(t, totalSize/pageSize-1, diffMeta.Empty.GetCardinality())

	got := make([]byte, pageSize)
	_, err = dedupCache.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, zeroPage, got)
}

func TestCacheDedup_ContextCancellation(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		totalSize = blockSize * 8
	)

	data := make([]byte, totalSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, _, err = src.Dedup(
		ctx,
		&fakeOriginalDevice{data: data, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.ErrorIs(t, err, context.Canceled)
}

// Run-coalescing tests covering match/differ patterns within a single block.
// These exercise the runStart/flushRun logic in Cache.Dedup's second pass:
// the inner loop coalesces consecutive differing pages into one
// copy_file_range call, flushed either by an intervening matching page or
// by the closing flushRun(blockSize) at the end of the block.
func TestCacheDedup_RunCoalescing(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize
	)

	cases := []struct {
		name      string
		differing []int64 // page indices within block 0 that differ from base
	}{
		// M M D D — closing flushRun(blockSize) flushes a 2-page run.
		{"trailing multi-page run", []int64{2, 3}},
		// M D D M — page 3 matching triggers an interior flush of a 2-page run.
		{"interior multi-page run", []int64{1, 2}},
		// D M D M — two single-page runs; first flushed by page 1, second by closing flushRun.
		{"alternating", []int64{0, 2}},
		// D D D D — one 4-page run flushed by closing flushRun(blockSize).
		{"full block run", []int64{0, 1, 2, 3}},
		// D D M D — interior 2-page flush, then a closing 1-page flush.
		{"leading-and-trailing", []int64{0, 1, 3}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			origData := make([]byte, totalSize)
			_, err := rand.Read(origData)
			require.NoError(t, err)
			srcData := make([]byte, totalSize)
			copy(srcData, origData)

			contents := map[int64][]byte{}
			for _, p := range tc.differing {
				c := bytes.Repeat([]byte{byte(0xA0 + p)}, int(pageSize))
				copy(srcData[p*pageSize:], c)
				contents[p] = c
			}

			dirty := roaring.New()
			dirty.Add(0)

			src := buildPackedSrcCache(t, srcData, dirty, blockSize)
			dedup, meta, err := src.Dedup(
				t.Context(),
				&fakeOriginalDevice{data: origData, blockSize: blockSize},
				dirty,
				blockSize,
				totalSize,
				t.TempDir()+"/dedup-cache",
			)
			require.NoError(t, err)
			t.Cleanup(func() { _ = dedup.Close() })

			require.EqualValues(t, len(tc.differing), meta.Dirty.GetCardinality())
			for _, p := range tc.differing {
				require.True(t, meta.Dirty.Contains(uint32(p)), "page %d expected in Dirty", p)
			}
			require.EqualValues(t, totalSize/pageSize-int64(len(tc.differing)), meta.Empty.GetCardinality())

			// Pages must be packed in iteration order (ascending page index).
			got := make([]byte, pageSize)
			for i, p := range tc.differing {
				_, err := dedup.ReadAt(got, int64(i)*pageSize)
				require.NoError(t, err)
				require.Equal(t, contents[p], got, "packed offset %d should hold page %d", i, p)
			}
		})
	}
}

// The very last page of memory is dirty — guards against off-by-one in
// pageEmpty.Flip(0, totalPageCount).
func TestCacheDedup_LastPageBoundary(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		pageSize  = int64(header.PageSize)
		totalSize = blockSize * 2 // 8 pages, last page index = 7
	)

	origData := make([]byte, totalSize)
	_, err := rand.Read(origData)
	require.NoError(t, err)
	srcData := make([]byte, totalSize)
	copy(srcData, origData)

	differing := bytes.Repeat([]byte{0xEE}, int(pageSize))
	copy(srcData[7*pageSize:], differing)

	dirty := roaring.New()
	dirty.Add(1) // only block containing the last page

	src := buildPackedSrcCache(t, srcData, dirty, blockSize)
	dedup, meta, err := src.Dedup(
		t.Context(),
		&fakeOriginalDevice{data: origData, blockSize: blockSize},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dedup.Close() })

	require.EqualValues(t, 1, meta.Dirty.GetCardinality())
	require.True(t, meta.Dirty.Contains(7), "last page must be in Dirty")
	require.EqualValues(t, totalSize/pageSize-1, meta.Empty.GetCardinality())
	require.False(t, meta.Empty.Contains(7), "last page must not be Empty")

	got := make([]byte, pageSize)
	_, err = dedup.ReadAt(got, 0)
	require.NoError(t, err)
	require.Equal(t, differing, got)
}

// erroringOriginalDevice always returns sentinelErr from ReadAt, used to
// verify that Cache.Dedup propagates failures from the base memory file.
type erroringOriginalDevice struct {
	*fakeOriginalDevice
	err error
}

func (e *erroringOriginalDevice) ReadAt(context.Context, []byte, int64) (int, error) {
	return 0, e.err
}

func TestCacheDedup_OriginalMemfileReadError(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		totalSize = blockSize * 2
	)

	data := make([]byte, totalSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	sentinel := errors.New("read failed")
	_, _, err = src.Dedup(
		t.Context(),
		&erroringOriginalDevice{
			fakeOriginalDevice: &fakeOriginalDevice{data: data, blockSize: blockSize},
			err:                sentinel,
		},
		dirty,
		blockSize,
		totalSize,
		t.TempDir()+"/dedup-cache",
	)
	require.ErrorIs(t, err, sentinel)
}

// hookingOriginalDevice invokes onRead before each delegated ReadAt. Used to
// drive context cancellation at a precise point during Cache.Dedup.
type hookingOriginalDevice struct {
	*fakeOriginalDevice
	onRead func()
}

func (h *hookingOriginalDevice) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	h.onRead()
	return h.fakeOriginalDevice.ReadAt(ctx, p, off)
}

// Cancellation observed by the second pass (the existing test only hits the
// first-pass branch). We cancel after the last first-pass read so the
// second pass enters its loop with the context already cancelled.
func TestCacheDedup_ContextCancellation_DuringSecondPass(t *testing.T) {
	t.Parallel()

	const (
		blockSize = int64(4 * header.PageSize)
		totalSize = blockSize * 2
	)

	data := make([]byte, totalSize)
	_, err := rand.Read(data)
	require.NoError(t, err)

	dirty := allBlocksDirty(uint32(totalSize / blockSize))
	src := buildPackedSrcCache(t, data, dirty, blockSize)

	ctx, cancel := context.WithCancel(t.Context())
	hook := &hookingOriginalDevice{
		fakeOriginalDevice: &fakeOriginalDevice{data: data, blockSize: blockSize},
	}
	calls := 0
	firstPassReads := int(totalSize / blockSize)
	hook.onRead = func() {
		calls++
		if calls == firstPassReads {
			cancel()
		}
	}

	_, _, err = src.Dedup(ctx, hook, dirty, blockSize, totalSize, t.TempDir()+"/dedup-cache")
	require.ErrorIs(t, err, context.Canceled)
}

// Direct test for the user-space fallback branch in copyFileRangeWithFallback.
// On standard local filesystems unix.CopyFileRange succeeds, so the fallback
// path is otherwise unreachable from the Cache.Dedup tests.
func TestCopyFileRangeWithFallback_FallbackPath(t *testing.T) {
	t.Parallel()

	pageSize := int(header.PageSize)
	payload := make([]byte, 3*pageSize)
	_, err := rand.Read(payload)
	require.NoError(t, err)

	dir := t.TempDir()
	srcPath := dir + "/src"
	dstPath := dir + "/dst"
	require.NoError(t, os.WriteFile(srcPath, payload, 0o600))
	require.NoError(t, os.WriteFile(dstPath, make([]byte, 4*pageSize), 0o600))

	src, err := os.Open(srcPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = src.Close() })

	dst, err := os.OpenFile(dstPath, os.O_RDWR, 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = dst.Close() })

	// Force the fallback branch and copy 2 pages from src offset pageSize
	// into dst offset 2*pageSize. Non-zero starting offsets catch
	// off-by-one in the io.Copy advancement of *srcOff / *dstOff.
	srcOff := int64(pageSize)
	dstOff := int64(2 * pageSize)
	fallback := true
	require.NoError(t, copyFileRangeWithFallback(t.Context(), src, dst, &srcOff, &dstOff, 2*pageSize, &fallback))

	require.EqualValues(t, 3*pageSize, srcOff)
	require.EqualValues(t, 4*pageSize, dstOff)
	require.True(t, fallback, "fallback flag must remain set after a fallback copy")

	got, err := os.ReadFile(dstPath)
	require.NoError(t, err)
	require.Equal(t, payload[pageSize:3*pageSize], got[2*pageSize:4*pageSize])
	require.Equal(t, make([]byte, 2*pageSize), got[:2*pageSize], "untouched prefix of dst must remain zero")
}

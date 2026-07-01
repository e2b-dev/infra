//go:build linux

package block

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

// fakeOriginalDevice satisfies ReadonlyDevice over a fixed byte buffer.
// Tracks Slice/ReadAt calls so dedup tests can assert fast-path skipping.
type fakeOriginalDevice struct {
	data  []byte
	hdr   *header.Header // optional; nil disables the dedup fast paths
	reads int
}

func (f *fakeOriginalDevice) ReadAt(_ context.Context, p []byte, off int64) (int, error) {
	f.reads++
	if off >= int64(len(f.data)) {
		return 0, io.EOF
	}
	n := copy(p, f.data[off:])
	if n < len(p) {
		return n, io.EOF
	}

	return n, nil
}

func (f *fakeOriginalDevice) Slice(_ context.Context, off, length int64) ([]byte, error) {
	f.reads++
	if off+length > int64(len(f.data)) {
		return nil, io.EOF
	}

	return f.data[off : off+length], nil
}

func (f *fakeOriginalDevice) Size(context.Context) (int64, error) { return int64(len(f.data)), nil }
func (f *fakeOriginalDevice) Close() error                        { return nil }
func (f *fakeOriginalDevice) BlockSize() int64                    { return int64(header.PageSize) }
func (f *fakeOriginalDevice) Header() *header.Header              { return f.hdr }
func (f *fakeOriginalDevice) SwapHeader(*header.Header)           {}

// erroringOriginalDevice returns sentinel from every ReadAt and Slice.
type erroringOriginalDevice struct {
	fakeOriginalDevice

	err error
}

func (e *erroringOriginalDevice) ReadAt(context.Context, []byte, int64) (int, error) {
	return 0, e.err
}

func (e *erroringOriginalDevice) Slice(context.Context, int64, int64) ([]byte, error) {
	return nil, e.err
}

// peekingOriginalDevice wraps fakeOriginalDevice with a programmable
// CachePeeker implementation, so dedup best-effort tests can force a
// "uncached" answer without touching real chunkers.
type peekingOriginalDevice struct {
	fakeOriginalDevice

	cached bool
}

func (p *peekingOriginalDevice) IsCached(context.Context, int64, int64) bool { return p.cached }

// pwritevAll must concatenate non-contiguous iovecs at off and survive a
// kernel short-write (the helper retries with the remaining tail).
func TestPwritevAllConcatenatesIovecs(t *testing.T) {
	t.Parallel()

	path := t.TempDir() + "/out"
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	a := bytes.Repeat([]byte{0xAA}, 13)
	b := bytes.Repeat([]byte{0xBB}, 7)
	c := bytes.Repeat([]byte{0xCC}, 5)

	require.NoError(t, pwritevAll(int(f.Fd()), 42, [][]byte{a, b, c}))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	expected := append(append(make([]byte, 42), a...), append(b, c...)...)
	require.Equal(t, expected, got)
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

// Async: the copy detaches from the request context (cancelling the parent
// ctx doesn't abort it). After Wait the file on disk has the full payload.
func TestNewCacheFromMemfdAsync_DetachesAndFlushes(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := uint32(16)
	memfd, expected := newTestMemfd(t, pageSize*int64(numPages))

	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	ctx, cancel := context.WithCancel(t.Context())
	cachePath := t.TempDir() + "/cache"
	cache, err := NewCacheFromMemfdAsync(ctx, pageSize, cachePath, memfd, dirty)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cancel()
	require.NoError(t, cache.Wait(t.Context()))

	fromFile, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	require.Equal(t, expected, fromFile)
}

// Compare detaches: the header future resolves only after the goroutine
// runs, and the deduped cache (after Wait) holds only pages that differ.
func TestNewCacheFromMemfdDeduped_DetachesCompareAndDrain(t *testing.T) {
	t.Parallel()

	pageSize := int64(header.PageSize)
	numPages := uint32(8)
	size := pageSize * int64(numPages)

	memfd, srcData := newTestMemfd(t, size)
	baseData := make([]byte, size)
	copy(baseData, srcData)
	for _, p := range []uint32{1, 4} {
		off := int64(p) * pageSize
		for i := range pageSize {
			baseData[off+i] ^= 0xFF
		}
	}

	dirty := roaring.New()
	dirty.AddRange(0, uint64(numPages))

	ctx, cancel := context.WithCancel(t.Context())
	cachePath := t.TempDir() + "/dedup-async"
	metaOut := utils.NewSetOnce[*header.DiffMetadata]()
	cache, err := NewCacheFromMemfdDeduped(
		ctx, &fakeOriginalDevice{data: baseData}, pageSize, cachePath, memfd, dirty, false, false,
		DedupBudget{}, nil, metaOut, false,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cache.Close() })

	cancel()

	meta, err := metaOut.WaitWithContext(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 2, meta.Dirty.GetCardinality())

	_, err = cache.Wait(t.Context())
	require.NoError(t, err)

	got := make([]byte, pageSize*2)
	_, err = cache.ReadAt(got, 0)
	require.NoError(t, err)
	expected := append([]byte{}, srcData[pageSize:pageSize*2]...)
	expected = append(expected, srcData[pageSize*4:pageSize*5]...)
	require.Equal(t, expected, got)
}

// buildPackedIndex.translate maps a packed diff offset back to the absolute
// memfd offset for the dirty run it belongs to, and refuses ranges that cross
// a run boundary or fall outside any run.
func TestPackedIndexTranslate(t *testing.T) {
	t.Parallel()

	ps := int64(header.PageSize)
	// Dirty pages 0, 2, 5 pack contiguously as seg0=[0,ps)->abs 0,
	// seg1=[ps,2ps)->abs 2ps, seg2=[2ps,3ps)->abs 5ps.
	dirty := roaring.New()
	dirty.AddMany([]uint32{0, 2, 5})
	idx := buildPackedIndex(dirty)

	for _, tc := range []struct{ packed, abs int64 }{{0, 0}, {ps, 2 * ps}, {2 * ps, 5 * ps}} {
		abs, ok := idx.translate(tc.packed, ps)
		require.True(t, ok)
		require.Equal(t, tc.abs, abs)
	}

	// A range spanning two runs can't be served from one contiguous memfd span.
	_, ok := idx.translate(0, 2*ps)
	require.False(t, ok)
	// Past the last run.
	_, ok = idx.translate(3*ps, ps)
	require.False(t, ok)
}

// While the drain is in progress (done unresolved), reads are served from the
// still-mapped memfd via the packed→absolute index; once done resolves the
// inflight path is bypassed in favor of the drained cache.
func TestDedupedMemfdCache_InflightServesFromMemfd(t *testing.T) {
	t.Parallel()

	ps := int64(header.PageSize)
	memfd, data := newTestMemfd(t, ps*6)
	t.Cleanup(func() { _ = memfd.Close() })

	// Non-adjacent dirty set so packed offsets differ from absolute offsets.
	dirty := roaring.New()
	dirty.AddMany([]uint32{0, 2, 5})

	// Construct the post-compare, pre-drain state directly (no goroutine): the
	// memfd + index are published and done is unresolved.
	d := &DedupedMemfdCache{
		done:     utils.NewSetOnce[*Cache](),
		inflight: true,
		memfd:    memfd,
		index:    buildPackedIndex(dirty),
	}

	// ReadAt at packed offsets resolves to the right absolute memfd pages.
	for i, srcPage := range []int64{0, 2, 5} {
		got := make([]byte, ps)
		n, err := d.ReadAt(got, int64(i)*ps)
		require.NoError(t, err)
		require.Equal(t, int(ps), n)
		require.Equal(t, data[srcPage*ps:(srcPage+1)*ps], got)
	}

	// Slice takes the same path and returns a copy of the memfd bytes.
	s, err := d.Slice(ps, ps) // packed page 1 -> absolute page 2
	require.NoError(t, err)
	require.Equal(t, data[2*ps:3*ps], s)

	// Once the drain resolves done, the inflight path is bypassed.
	require.NoError(t, d.done.SetValue(nil))
	_, ok := d.tryInflightRead(make([]byte, ps), 0)
	require.False(t, ok)
}

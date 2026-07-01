//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// Memfd wraps a memfd received from Firecracker. NewFromFd takes ownership of
// the fd and mmaps it; Close releases both.
type Memfd struct {
	fd   int
	mmap []byte
}

func NewFromFd(fd int) (*Memfd, error) {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("fstat memfd: %w", err)
	}
	b, err := unix.Mmap(fd, 0, int(st.Size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("mmap memfd: %w", err)
	}

	return &Memfd{fd: fd, mmap: b}, nil
}

// Slice returns a zero-copy view of [offset, offset+size). Valid until Close.
func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if offset < 0 || offset+size > int64(len(m.mmap)) {
		return nil, fmt.Errorf("range [%d, %d) out of bounds (size %d)", offset, offset+size, len(m.mmap))
	}

	return m.mmap[offset : offset+size], nil
}

// Close releases the mmap and the fd. Single-use: every Memfd has exactly
// one owner (NewCacheFromMemfd consumes it during construction; the UFFD
// handshake transfers ownership via atomic Swap), so we don't guard against
// double-close.
func (m *Memfd) Close() error {
	var err error
	if e := unix.Munmap(m.mmap); e != nil {
		err = fmt.Errorf("munmap memfd: %w", e)
	}
	if e := unix.Close(m.fd); e != nil {
		err = errors.Join(err, fmt.Errorf("close memfd: %w", e))
	}

	return err
}

// NewCacheFromMemfd builds a Cache populated from a memfd. The memfd is
// consumed and closed during construction.
func NewCacheFromMemfd(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*Cache, error) {
	ctx, span := tracer.Start(ctx, "export-memory-from-memfd",
		trace.WithAttributes(
			attribute.Bool("async", false),
		),
	)
	defer span.End()

	cache, err := NewCache(int64(dirty.GetCardinality())*blockSize, blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}
	if err := copyFromMemfd(ctx, cache, memfd, dirty, blockSize); err != nil {
		return nil, errors.Join(err, memfd.Close(), cache.Close())
	}
	if err := memfd.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close memfd: %w", err), cache.Close())
	}

	return cache, nil
}

func copyFromMemfd(ctx context.Context, cache *Cache, memfd *Memfd, dirty *roaring.Bitmap, blockSize int64) error {
	var cacheOff int64
	for r := range BitsetRanges(dirty, blockSize) {
		if err := ctx.Err(); err != nil {
			return err
		}

		src, err := memfd.Slice(r.Start, r.Size)
		if err != nil {
			return fmt.Errorf("memfd slice [%d,%d): %w", r.Start, r.Start+r.Size, err)
		}

		copy((*cache.mmap)[cacheOff:cacheOff+r.Size], src)
		cache.setIsCached(cacheOff, r.Size)
		cacheOff += r.Size
	}

	return nil
}

// MemfdCache wraps a Cache populated from a memfd on a background
// goroutine. Reads block on the copy via Wait. Once it finishes, runCopy
// closes the memfd (releasing the hugetlb pages — potentially tens of GB)
// and reads delegate to the embedded Cache.
type MemfdCache struct {
	cache  *Cache
	cancel context.CancelFunc
	done   *utils.SetOnce[struct{}]
}

// NewCacheFromMemfdAsync starts the memfd→cache copy on a goroutine so
// Pause can return as soon as the snapshot file + diff metadata are
// written. The returned wrapper takes ownership of memfd; runCopy closes
// it when the copy completes (or is cancelled via Close).
func NewCacheFromMemfdAsync(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*MemfdCache, error) {
	ctx, span := tracer.Start(ctx, "export-memory-from-memfd",
		trace.WithAttributes(
			attribute.Bool("async", true),
		),
	)
	defer span.End()

	cache, err := NewCache(int64(dirty.GetCardinality())*blockSize, blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}
	done := utils.NewSetOnce[struct{}]()
	if dirty.IsEmpty() {
		if closeErr := memfd.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close memfd: %w", closeErr), cache.Close())
		}
		_ = done.SetValue(struct{}{})

		return &MemfdCache{cache: cache, done: done}, nil
	}

	// Detach from the request context so the copy outlives Pause.
	copyCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m := &MemfdCache{cache: cache, cancel: cancel, done: done}

	go m.runCopy(copyCtx, memfd, dirty, blockSize)

	return m, nil
}

func (m *MemfdCache) runCopy(ctx context.Context, memfd *Memfd, dirty *roaring.Bitmap, blockSize int64) {
	err := copyFromMemfd(ctx, m.cache, memfd, dirty, blockSize)
	if closeErr := memfd.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close memfd: %w", closeErr))
	}
	_ = m.done.SetResult(struct{}{}, err)
}

// Wait blocks until the background copy completes (or ctx is cancelled).
func (m *MemfdCache) Wait(ctx context.Context) error {
	_, err := m.done.WaitWithContext(ctx)

	return err
}

func (m *MemfdCache) ReadAt(b []byte, off int64) (int, error) {
	if err := m.Wait(context.Background()); err != nil {
		return 0, err
	}

	return m.cache.ReadAt(b, off)
}

func (m *MemfdCache) Slice(off, length int64) ([]byte, error) {
	if err := m.Wait(context.Background()); err != nil {
		return nil, err
	}

	return m.cache.Slice(off, length)
}

func (m *MemfdCache) Close() error {
	if m.cancel != nil {
		m.cancel()
		<-m.done.Done
	}

	return m.cache.Close()
}

func (m *MemfdCache) Path(ctx context.Context) (string, error) {
	if err := m.Wait(ctx); err != nil {
		return "", err
	}

	return m.cache.filePath, nil
}

func (m *MemfdCache) FileSize(ctx context.Context) (int64, error) {
	if err := m.Wait(ctx); err != nil {
		return 0, err
	}

	return m.cache.FileSize(ctx)
}

func (m *MemfdCache) BlockSize() int64     { return m.cache.BlockSize() }
func (m *MemfdCache) Size() (int64, error) { return m.cache.Size() }

// DedupedMemfdCache runs compare+drain on a goroutine; metaOut resolves
// after compare, reads against the cache block on done until drain finishes.
//
// When inflight serving is enabled, reads are served directly from the
// still-mapped memfd during the drain window instead of blocking on done, so a
// resume overlapping the pause is not delayed by the dedup drain.
type DedupedMemfdCache struct {
	outPath string
	cancel  context.CancelFunc
	done    *utils.SetOnce[*Cache]

	inflight bool
	// mu guards memfd + index. memfd is non-nil only between "compare done"
	// (published in runDedup) and "drain done" (closed under the write lock so
	// it can't be unmapped under an in-flight reader). index translates a
	// packed diff-storage offset back to the absolute memfd offset.
	mu    sync.RWMutex
	memfd *Memfd
	index packedIndex
}

func NewCacheFromMemfdDeduped(
	ctx context.Context,
	base ReadonlyDevice,
	blockSize int64,
	outPath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
	bestEffort bool,
	directIO bool,
	budget DedupBudget,
	inputEmpty *roaring.Bitmap,
	metaOut *utils.SetOnce[*header.DiffMetadata],
	inflightServe bool,
) (*DedupedMemfdCache, error) {
	if blockSize%header.PageSize != 0 {
		return nil, fmt.Errorf("diff block size %d not a multiple of dedup page size %d", blockSize, header.PageSize)
	}
	drainCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	d := &DedupedMemfdCache{
		outPath:  outPath,
		cancel:   cancel,
		done:     utils.NewSetOnce[*Cache](),
		inflight: inflightServe,
	}
	go d.runDedup(drainCtx, base, blockSize, memfd, dirty, bestEffort, directIO, budget, inputEmpty, metaOut)

	return d, nil
}

// packedSeg maps a contiguous run of the packed diff artifact back to its
// absolute (device) memfd offset. absStart+delta is the memfd offset for a
// read at packedStart+delta.
type packedSeg struct {
	packedStart int64
	absStart    int64
	length      int64
}

// packedIndex is the ascending-by-packedStart list of dirty runs. It mirrors
// the packing done by dedupDrain (BitsetRanges over pageDirty at PageSize) and
// the BuildStorageOffset assignment in header.CreateMapping, so a packed
// offset resolves to the same absolute offset the deduped header would map to.
type packedIndex []packedSeg

func buildPackedIndex(pageDirty *roaring.Bitmap) packedIndex {
	var idx packedIndex
	var packed int64
	for r := range BitsetRanges(pageDirty, header.PageSize) {
		idx = append(idx, packedSeg{packedStart: packed, absStart: r.Start, length: r.Size})
		packed += r.Size
	}

	return idx
}

// translate maps [off, off+length) in packed space to an absolute memfd offset.
// It only succeeds when the whole range lies in a single dirty run (which is
// always the case for a single build.File read segment); otherwise the caller
// falls back to waiting for the drained cache.
func (idx packedIndex) translate(off, length int64) (int64, bool) {
	lo, hi := 0, len(idx)
	for lo < hi {
		mid := (lo + hi) / 2
		if idx[mid].packedStart+idx[mid].length <= off {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo >= len(idx) {
		return 0, false
	}
	seg := idx[lo]
	if off < seg.packedStart || off+length > seg.packedStart+seg.length {
		return 0, false
	}

	return seg.absStart + (off - seg.packedStart), true
}

func (d *DedupedMemfdCache) runDedup(
	ctx context.Context,
	base ReadonlyDevice,
	blockSize int64,
	memfd *Memfd,
	dirty *roaring.Bitmap,
	bestEffort, directIO bool,
	budget DedupBudget,
	inputEmpty *roaring.Bitmap,
	metaOut *utils.SetOnce[*header.DiffMetadata],
) {
	ctx, span := tracer.Start(ctx, "dedup-pages")
	defer span.End()

	src := func(absOff int64) ([]byte, error) { return memfd.Slice(absOff, blockSize) }

	compareStart := time.Now()
	plan, err := dedupCompare(ctx, src, base, dirty, blockSize, bestEffort, budget)
	compareDur := time.Since(compareStart)
	if err != nil {
		logSetOnceErr(ctx, "dedup metaOut", metaOut.SetError(err))
		logSetOnceErr(ctx, "dedup done", d.done.SetError(errors.Join(err, memfd.Close())))

		return
	}

	// Capture the scan-only zero count before inputEmpty is merged in place:
	// dedup.empty_pages must report content-detected zeros, not whole-VM
	// empties (cloning the bitmap to preserve it would be too expensive).
	scanEmptyPages := int64(plan.pageEmpty.GetCardinality())
	if inputEmpty != nil {
		ratio := uint64(blockSize / header.PageSize)
		for start, end := range inputEmpty.Ranges() {
			plan.pageEmpty.AddRange(uint64(start)*ratio, end*ratio)
		}
	}
	meta := &header.DiffMetadata{Dirty: plan.pageDirty, Empty: plan.pageEmpty, BlockSize: header.PageSize}
	// Whole-VM empty set recorded in the header (scan zeros + inputEmpty).
	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.header_empty_pages", int64(plan.pageEmpty.GetCardinality())))
	logSetOnceErr(ctx, "dedup metaOut", metaOut.SetValue(meta))

	// Publish the memfd + packed→absolute index so a resume overlapping this
	// pause serves dirty pages straight from the memfd during the drain window
	// instead of blocking on done. Reads take mu.RLock; the close below takes
	// mu.Lock, so the memfd is never unmapped under an in-flight reader.
	if d.inflight {
		d.mu.Lock()
		d.index = buildPackedIndex(plan.pageDirty)
		d.memfd = memfd
		d.mu.Unlock()
	}

	writeStart := time.Now()
	cache, err := dedupDrain(ctx, src, plan.pageDirty, blockSize, d.outPath, directIO)
	writeDur := time.Since(writeStart)
	d.mu.Lock()
	closeErr := memfd.Close()
	d.memfd = nil
	d.mu.Unlock()
	if closeErr != nil {
		logger.L().Warn(ctx, "close memfd after dedup drain", zap.Error(closeErr))
	}

	recordDedupAttrs(ctx, plan, scanEmptyPages, compareDur, writeDur)
	logSetOnceErr(ctx, "dedup done", d.done.SetResult(cache, err))
}

// logSetOnceErr warns on a SetOnce.SetValue/SetError failure (i.e. a
// repeated set), which signals a misuse rather than a runtime problem;
// keep going so the original outcome still wins.
func logSetOnceErr(ctx context.Context, what string, err error) {
	if err != nil {
		logger.L().Warn(ctx, "set once already resolved", zap.String("what", what), zap.Error(err))
	}
}

func (d *DedupedMemfdCache) Wait(ctx context.Context) (*Cache, error) {
	return d.done.WaitWithContext(ctx)
}

// tryInflightRead fills b from the memfd if inflight serving is active and the
// drain has not yet finished. Returns ok=false to fall back to the drained
// cache (inflight disabled, drain done, memfd already closed, or the range
// can't be resolved from a single dirty run).
func (d *DedupedMemfdCache) tryInflightRead(b []byte, off int64) (int, bool) {
	if !d.inflight {
		return 0, false
	}
	// Drain finished: the cache is authoritative, use it.
	select {
	case <-d.done.Done:
		return 0, false
	default:
	}

	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.memfd == nil || d.index == nil {
		return 0, false
	}
	absOff, ok := d.index.translate(off, int64(len(b)))
	if !ok {
		return 0, false
	}
	src, err := d.memfd.Slice(absOff, int64(len(b)))
	if err != nil {
		return 0, false
	}
	copy(b, src)

	return len(b), true
}

func (d *DedupedMemfdCache) ReadAt(b []byte, off int64) (int, error) {
	if n, ok := d.tryInflightRead(b, off); ok {
		return n, nil
	}

	c, err := d.Wait(context.Background())
	if err != nil {
		return 0, err
	}

	return c.ReadAt(b, off)
}

func (d *DedupedMemfdCache) Slice(off, length int64) ([]byte, error) {
	if d.inflight {
		buf := make([]byte, length)
		if _, ok := d.tryInflightRead(buf, off); ok {
			return buf, nil
		}
	}

	c, err := d.Wait(context.Background())
	if err != nil {
		return nil, err
	}

	return c.Slice(off, length)
}

func (d *DedupedMemfdCache) Close() error {
	d.cancel()
	c, _ := d.done.Wait()
	if c != nil {
		return c.Close()
	}
	_ = os.Remove(d.outPath)

	return nil
}

func (d *DedupedMemfdCache) Path(ctx context.Context) (string, error) {
	c, err := d.Wait(ctx)
	if err != nil {
		return "", err
	}

	return c.filePath, nil
}

func (d *DedupedMemfdCache) FileSize(ctx context.Context) (int64, error) {
	c, err := d.Wait(ctx)
	if err != nil {
		return 0, err
	}

	return c.FileSize(ctx)
}

func (d *DedupedMemfdCache) BlockSize() int64 { return header.PageSize }
func (d *DedupedMemfdCache) Size() (int64, error) {
	c, err := d.Wait(context.Background())
	if err != nil {
		return 0, err
	}

	return c.Size()
}

// pwritevAll writes the iovecs at off, handling EINTR and short writes by
// advancing through the list (slicing the first partially-written entry).
// Callers (drainIovs) keep |iovs| ≤ IOV_MAX. iovs is mutated in place.
func pwritevAll(fd int, off int64, iovs [][]byte) error {
	for len(iovs) > 0 {
		for len(iovs) > 0 && len(iovs[0]) == 0 {
			iovs = iovs[1:]
		}
		if len(iovs) == 0 {
			return nil
		}

		n, err := unix.Pwritev(fd, iovs, off)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("pwritev: no progress, %d iovec(s) remaining", len(iovs))
		}

		off += int64(n)
		for n > 0 && len(iovs) > 0 {
			if len(iovs[0]) <= n {
				n -= len(iovs[0])
				iovs = iovs[1:]
			} else {
				iovs[0] = iovs[0][n:]
				n = 0
			}
		}
	}

	return nil
}

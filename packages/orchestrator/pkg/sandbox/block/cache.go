//go:build linux

package block

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	oomMinBackoff = 100 * time.Millisecond
	oomMaxJitter  = 100 * time.Millisecond
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block")

type CacheClosedError struct {
	filePath string
}

func (e *CacheClosedError) Error() string {
	return fmt.Sprintf("block cache already closed for path %s", e.filePath)
}

func NewErrCacheClosed(filePath string) *CacheClosedError {
	return &CacheClosedError{
		filePath: filePath,
	}
}

type Cache struct {
	filePath  string
	size      int64
	blockSize int64
	file      *os.File
	mu        sync.RWMutex
	tracker   *Tracker // Dirty: payload in file; Zero: punched, emitted as Empty in the diff
	dirtyFile bool
	closed    atomic.Bool
}

func NewCache(size, blockSize int64, filePath string, dirtyFile bool) (*Cache, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	// This should create a sparse file on Linux.
	err = f.Truncate(size)
	if err != nil {
		_ = f.Close()

		return nil, fmt.Errorf("error allocating file: %w", err)
	}

	return &Cache{
		filePath:  filePath,
		size:      size,
		blockSize: blockSize,
		file:      f,
		dirtyFile: dirtyFile,
		tracker:   NewTracker(),
	}, nil
}

func writeFullAt(f *os.File, b []byte, off int64) error {
	for len(b) > 0 {
		n, err := f.WriteAt(b, off)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		off += int64(n)
		b = b[n:]
	}

	return nil
}

func (c *Cache) isClosed() bool {
	return c.closed.Load()
}

func (c *Cache) ExportToDiff(ctx context.Context, out *os.File) (*header.DiffMetadata, error) {
	ctx, childSpan := tracer.Start(ctx, "export-to-diff")
	defer childSpan.End()

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.isClosed() {
		return nil, NewErrCacheClosed(c.filePath)
	}

	if c.size == 0 {
		return header.NewDiffMetadata(c.blockSize, nil, nil), nil
	}

	src := int(c.file.Fd())

	// Calling sync_file_range marks the range for writeback and starts it early.
	// This is just an optimization, so if it fails just log a warning and let copy_file_range do the actual work.
	err := unix.SyncFileRange(src, 0, c.size, unix.SYNC_FILE_RANGE_WRITE)
	if err != nil {
		logger.L().Warn(ctx, "error syncing file", zap.Error(err))
	}

	dirty, empty := c.tracker.Export()
	diffMetadata := header.NewDiffMetadata(c.blockSize, dirty, empty)

	dst := int(out.Fd())
	var writeOffset int64
	var totalRanges int64
	fallback := false

	copyStart := time.Now()
	for r := range BitsetRanges(diffMetadata.Dirty, diffMetadata.BlockSize) {
		totalRanges++
		remaining := int(r.Size)
		readOffset := r.Start

		// The kernel may return short writes (e.g. capped at MAX_RW_COUNT on non-reflink filesystems),
		// so we loop until the full range is copied. The offset pointers are advanced by the kernel.
		for remaining > 0 {
			if !fallback {
				// On XFS this uses reflink automatically.
				n, err := unix.CopyFileRange(
					src,
					&readOffset,
					dst,
					&writeOffset,
					remaining,
					0,
				)
				switch {
				case errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS):
					fallback = true
					logger.L().Warn(ctx, "copy_file_range unsupported, falling back to normal copy", zap.Error(err))
				case err != nil:
					return nil, fmt.Errorf("error copying file range: %w", err)
				case n == 0:
					return nil, fmt.Errorf("copy_file_range returned 0 with %d bytes remaining", remaining)
				default:
					remaining -= n
				}
			}

			// CopyFileRange failed. Falling back to normal copy
			if fallback && remaining > 0 {
				if _, err := out.Seek(writeOffset, io.SeekStart); err != nil {
					return nil, fmt.Errorf("error seeking: %w", err)
				}
				sr := io.NewSectionReader(c.file, readOffset, int64(remaining))
				if _, err := io.Copy(out, sr); err != nil {
					return nil, fmt.Errorf("error copying file range. %w", err)
				}

				writeOffset += int64(remaining)
				remaining = 0
			}
		}
	}

	telemetry.SetAttributes(ctx,
		attribute.Int64("copy_ms", time.Since(copyStart).Milliseconds()),
		attribute.Int64("total_size_bytes", c.size),
		attribute.Int64("dirty_size_bytes", int64(diffMetadata.Dirty.GetCardinality())*c.blockSize),
		attribute.Int64("empty_size_bytes", int64(diffMetadata.Empty.GetCardinality())*c.blockSize),
		attribute.Int64("total_ranges", totalRanges),
	)

	return diffMetadata, nil
}

type dedupPlan struct {
	pageDirty    *roaring.Bitmap
	pageEmpty    *roaring.Bitmap
	exportedSize int64
}

// dedupCompare classifies each dirty page against base into pageDirty or
// pageEmpty. Per-page IsCached so a single uncached neighbour can't poison
// cached pages of the same block when the parent header is page-granular.
func dedupCompare(
	ctx context.Context,
	src func(absOff int64, p []byte) error,
	base ReadonlyDevice,
	dirty *roaring.Bitmap,
	blockSize int64,
	bestEffort bool,
) (*dedupPlan, error) {
	pageDirty := roaring.New()
	pageEmpty := roaring.New()
	var exportedSize int64

	baseHeader := base.Header()
	peeker, _ := base.(CachePeeker)

	for r := range BitsetRanges(dirty, blockSize) {
		exportedSize += r.Size

		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			if err := ctx.Err(); err != nil {
				return nil, err
			}

			absOff := r.Start + chunkOff
			srcBuf := make([]byte, blockSize)
			if err := src(absOff, srcBuf); err != nil {
				return nil, err
			}

			for i := int64(0); i < blockSize; i += header.PageSize {
				srcPage := srcBuf[i : i+header.PageSize]
				pageIdx := uint32((absOff + i) / header.PageSize)
				pageOff := absOff + i

				if header.IsZero(srcPage) {
					pageEmpty.Add(pageIdx)

					continue
				}

				skip := false
				if baseHeader != nil {
					if m, err := baseHeader.GetShiftedMapping(ctx, pageOff); err == nil {
						if m.BuildId == uuid.Nil && int64(m.Length) >= header.PageSize {
							skip = true
						}
					}
				}
				if !skip && bestEffort && peeker != nil && !peeker.IsCached(ctx, pageOff, header.PageSize) {
					skip = true
				}
				if skip {
					pageDirty.Add(pageIdx)

					continue
				}

				basePage := make([]byte, header.PageSize)
				if _, err := base.ReadAt(ctx, basePage, pageOff); err != nil {
					return nil, fmt.Errorf("read base at %d: %w", pageOff, err)
				}
				if bytes.Equal(srcPage, basePage) {
					continue
				}

				pageDirty.Add(pageIdx)
			}
		}
	}

	return &dedupPlan{pageDirty: pageDirty, pageEmpty: pageEmpty, exportedSize: exportedSize}, nil
}

// dedupDrain writes pageDirty pages from src to outPath packed at PageSize.
func dedupDrain(
	ctx context.Context,
	src func(absOff int64, p []byte) error,
	pageDirty *roaring.Bitmap,
	blockSize int64,
	outPath string,
	directIO bool,
) (*Cache, error) {
	openFlags := os.O_RDWR | os.O_CREATE
	if directIO {
		openFlags |= unix.O_DIRECT
	}
	f, err := os.OpenFile(outPath, openFlags, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open dedup cache: %w", err)
	}
	if want := int64(pageDirty.GetCardinality()) * header.PageSize; directIO && want > 0 {
		if fErr := unix.Fallocate(int(f.Fd()), 0, 0, want); fErr != nil {
			logger.L().Warn(ctx, "fallocate dedup cache; proceeding without preallocation", zap.Error(fErr))
		}
	}

	fileOff, err := drainDirtyPages(ctx, int(f.Fd()), src, pageDirty, blockSize)
	if err != nil {
		return nil, errors.Join(err, f.Close(), os.Remove(outPath))
	}

	if directIO {
		if err := f.Truncate(fileOff); err != nil {
			return nil, errors.Join(fmt.Errorf("truncate dedup cache: %w", err), f.Close(), os.Remove(outPath))
		}
	}
	if err := f.Close(); err != nil {
		return nil, errors.Join(err, os.Remove(outPath))
	}

	cache, err := NewCache(fileOff, header.PageSize, outPath, false)
	if err != nil {
		return nil, errors.Join(err, os.Remove(outPath))
	}
	cache.setIsCached(0, fileOff)

	return cache, nil
}

func recordDedupAttrs(ctx context.Context, totalPages, uniquePages, emptyPages int64, compareDur, writeDur time.Duration) {
	dedupedPages := totalPages - uniquePages - emptyPages
	ratio := 0.0
	if totalPages > 0 {
		ratio = float64(dedupedPages) / float64(totalPages)
	}
	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.total_pages", totalPages),
		attribute.Int64("dedup.deduped_pages", dedupedPages),
		attribute.Int64("dedup.unique_pages", uniquePages),
		attribute.Int64("dedup.empty_pages", emptyPages),
		attribute.Float64("dedup.ratio", ratio),
		attribute.Int64("dedup.compare_ms", compareDur.Milliseconds()),
		attribute.Int64("dedup.write_ms", writeDur.Milliseconds()),
	)
}

// drainDirtyPages packs pageDirty pages from src into fd. Mirrors
// Cache.copyProcessMemory: coalesce contiguous pages into ranges, carve at
// source-block boundaries, pre-split over MAX_RW_COUNT, then drainIovs.
func drainDirtyPages(ctx context.Context, fd int, src func(absOff int64, p []byte) error, pageDirty *roaring.Bitmap, blockSize int64) (int64, error) {
	var ranges []Range
	for r := range BitsetRanges(pageDirty, header.PageSize) {
		for off := r.Start; off < r.End(); {
			blockOff := (off / blockSize) * blockSize
			chunkEnd := min(r.End(), blockOff+blockSize)
			ranges = append(ranges, Range{Start: off, Size: chunkEnd - off})
			off = chunkEnd
		}
	}
	ranges = splitOversizedRanges(ranges, getAlignedMaxRwCount(header.PageSize))

	if err := drainIovs(ranges, func(r Range) int64 { return r.Size }, header.PageSize,
		func(destOff int64, batch []Range, _ int64) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			iovs := make([][]byte, len(batch))
			for i, r := range batch {
				blockOff := (r.Start / blockSize) * blockSize
				buf := make([]byte, blockSize)
				if err := src(blockOff, buf); err != nil {
					return fmt.Errorf("read src at %d: %w", blockOff, err)
				}
				iovs[i] = buf[r.Start-blockOff : r.Start-blockOff+r.Size]
			}
			if err := pwritevAll(fd, destOff, iovs); err != nil {
				return fmt.Errorf("pwritev dedup pages: %w", err)
			}

			return nil
		}); err != nil {
		return 0, err
	}

	return GetSize(ranges), nil
}

// Dedup writes pages from c that differ from base, packed at PageSize, to
// outPath. bestEffort skips uncached blocks; directIO uses O_DIRECT.
func (c *Cache) Dedup(
	ctx context.Context,
	base ReadonlyDevice,
	dirty *roaring.Bitmap,
	blockSize int64,
	outPath string,
	bestEffort bool,
	directIO bool,
) (*Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "dedup-pages")
	defer span.End()

	// c is packed in BitsetRanges order; map abs offset → packed offset.
	packed := make(map[int64]int64, dirty.GetCardinality())
	var cum int64
	for r := range BitsetRanges(dirty, blockSize) {
		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			packed[r.Start+chunkOff] = cum
			cum += blockSize
		}
	}
	src := func(absOff int64, p []byte) error {
		idx, ok := packed[absOff]
		if !ok {
			return fmt.Errorf("dedup src: %d not packed", absOff)
		}

		_, err := c.ReadAt(p, idx)

		return err
	}

	compareStart := time.Now()
	plan, err := dedupCompare(ctx, src, base, dirty, blockSize, bestEffort)
	if err != nil {
		return nil, nil, err
	}
	compareDur := time.Since(compareStart)

	writeStart := time.Now()
	cache, err := dedupDrain(ctx, src, plan.pageDirty, blockSize, outPath, directIO)
	if err != nil {
		return nil, nil, err
	}
	recordDedupAttrs(ctx,
		plan.exportedSize/header.PageSize,
		int64(plan.pageDirty.GetCardinality()),
		int64(plan.pageEmpty.GetCardinality()),
		compareDur, time.Since(writeStart),
	)

	return cache, &header.DiffMetadata{
		Dirty:     plan.pageDirty,
		Empty:     plan.pageEmpty,
		BlockSize: header.PageSize,
	}, nil
}

func (c *Cache) ReadAt(b []byte, off int64) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.readAtWithoutLock(b, off, false)
}

func (c *Cache) readAtWithoutLock(b []byte, off int64, skipCachedCheck bool) (int, error) {
	if len(b) == 0 || c.size == 0 {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}
	if off < 0 || off >= c.size {
		return 0, BytesNotAvailableError{}
	}

	if !skipCachedCheck && !c.dirtyFile && !c.isCached(off, int64(len(b))) {
		return 0, BytesNotAvailableError{}
	}

	n, err := c.file.ReadAt(b, off)
	if errors.Is(err, io.EOF) && n > 0 {
		err = nil
	}

	return n, err
}

func (c *Cache) WriteAt(b []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(b) == 0 || c.size == 0 {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	if int64(len(b))%c.blockSize != 0 || off%c.blockSize != 0 {
		return 0, fmt.Errorf("misaligned write: len=%d off=%d block=%d", len(b), off, c.blockSize)
	}

	end := min(off+int64(len(b)), c.size)
	if end <= off {
		return 0, nil
	}

	// detect-zeroes=unmap: coalesce contiguous same-state blocks into one bulk
	// copy or punchHole call. Caller must pass a block-aligned write (NBD invariant).
	flush := func(runStart, runEnd int64, runZero bool) error {
		startIdx := uint32(header.BlockIdx(runStart, c.blockSize))
		endIdx := uint32(header.BlockCeilIdx(runEnd, c.blockSize))
		if runZero {
			if err := c.punchHole(runStart, runEnd-runStart); err != nil {
				return err
			}
			c.tracker.SetRange(startIdx, endIdx, Zero)
		} else {
			if err := writeFullAt(c.file, b[runStart-off:runEnd-off], runStart); err != nil {
				return err
			}
			c.tracker.SetRange(startIdx, endIdx, Dirty)
		}

		return nil
	}

	runStart := off
	runZero := header.IsZero(b[:c.blockSize])
	for i := off + c.blockSize; i < end; i += c.blockSize {
		z := header.IsZero(b[i-off : i-off+c.blockSize])
		if z == runZero {
			continue
		}
		if err := flush(runStart, i, runZero); err != nil {
			return 0, err
		}
		runStart = i
		runZero = z
	}
	if err := flush(runStart, end, runZero); err != nil {
		return 0, err
	}

	return int(end - off), nil
}

func (c *Cache) writeRawAt(b []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(b) == 0 || c.size == 0 {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	end := min(off+int64(len(b)), c.size)
	if end <= off {
		return 0, nil
	}
	b = b[:end-off]
	if err := writeFullAt(c.file, b, off); err != nil {
		return 0, err
	}

	return len(b), nil
}

func (c *Cache) Close() (e error) {
	succ := c.closed.CompareAndSwap(false, true)
	if !succ {
		return NewErrCacheClosed(c.filePath)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.file != nil {
		e = errors.Join(e, c.file.Close())
	}

	// TODO: Move to to the scope of the caller
	e = errors.Join(e, os.RemoveAll(c.filePath))

	return e
}

func (c *Cache) Size() (int64, error) {
	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	return c.size, nil
}

// Slice returns a copy of the cached bytes.
func (c *Cache) Slice(off, length int64) ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return c.slice(off, length, false)
}

func (c *Cache) slice(off, length int64, skipCachedCheck bool) ([]byte, error) {
	end := min(off+length, c.size)
	if off < 0 || off >= end {
		return nil, BytesNotAvailableError{}
	}

	out := make([]byte, end-off)
	n, err := c.readAtWithoutLock(out, off, skipCachedCheck)
	if err != nil {
		return nil, err
	}

	return out[:n], nil
}

// Zero blocks are treated as cached: the file range reads back as zero (punched).
func (c *Cache) isCached(off, length int64) bool {
	start := uint32(header.BlockIdx(off, c.blockSize))
	end := uint32(header.BlockCeilIdx(min(off+length, c.size), c.blockSize))

	return c.tracker.Present(start, end)
}

func (c *Cache) setIsCached(off, length int64) {
	start := uint32(header.BlockIdx(off, c.blockSize))
	end := uint32(header.BlockCeilIdx(off+length, c.blockSize))

	c.tracker.SetRange(start, end, Dirty)
}

// punchHole frees backing pages; zero-write fallback if hole punching is unsupported.
func (c *Cache) punchHole(off, length int64) error {
	if length <= 0 {
		return nil
	}

	if err := unix.Fallocate(int(c.file.Fd()), unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, off, length); err == nil {
		return nil
	}

	const zeroChunkSize = 1 << 20
	zeroes := make([]byte, min(length, zeroChunkSize))
	for written := int64(0); written < length; {
		n := min(int64(len(zeroes)), length-written)
		if err := writeFullAt(c.file, zeroes[:n], off+written); err != nil {
			return err
		}
		written += n
	}

	return nil
}

// WriteZeroesAt punches the range and marks all touched blocks Zero.
// Caller must pass a block-aligned offset/length (NBD invariant).
func (c *Cache) WriteZeroesAt(off, length int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if length == 0 || c.size == 0 {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	end := min(off+length, c.size)
	if end <= off {
		return 0, nil
	}

	if err := c.punchHole(off, end-off); err != nil {
		return 0, err
	}
	c.tracker.SetRange(
		uint32(header.BlockIdx(off, c.blockSize)),
		uint32(header.BlockCeilIdx(end, c.blockSize)),
		Zero,
	)

	return int(end - off), nil
}

// FileSize returns the size of the cache on disk.
// The size might differ from the dirty size, as it may not be fully on disk.
func (c *Cache) FileSize(_ context.Context) (int64, error) {
	var stat syscall.Stat_t
	err := syscall.Stat(c.filePath, &stat)
	if err != nil {
		return 0, fmt.Errorf("failed to get file stats: %w", err)
	}

	var fsStat syscall.Statfs_t
	err = syscall.Statfs(c.filePath, &fsStat)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk stats for path %s: %w", c.filePath, err)
	}

	return stat.Blocks * fsStat.Bsize, nil
}

func (c *Cache) BlockSize() int64 {
	return c.blockSize
}

func (c *Cache) Path(_ context.Context) (string, error) {
	return c.filePath, nil
}

func NewCacheFromProcessMemory(
	ctx context.Context,
	blockSize int64,
	filePath string,
	pid int,
	ranges []Range,
) (*Cache, error) {
	size := GetSize(ranges)

	cache, err := NewCache(size, blockSize, filePath, false)
	if err != nil {
		return nil, err
	}

	if size == 0 {
		return cache, nil
	}

	err = cache.copyProcessMemory(ctx, pid, ranges)
	if err != nil {
		return nil, fmt.Errorf("failed to copy process memory: %w", errors.Join(err, cache.Close()))
	}

	return cache, nil
}

func (c *Cache) copyProcessMemory(
	ctx context.Context,
	pid int,
	rs []Range,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return NewErrCacheClosed(c.filePath)
	}

	// Pre-split so no single iov exceeds MAX_RW_COUNT.
	ranges := splitOversizedRanges(rs, getAlignedMaxRwCount(c.blockSize))

	return drainIovs(ranges, func(r Range) int64 { return r.Size }, c.blockSize,
		func(off int64, batch []Range, batchBytes int64) error {
			remote := make([]unix.RemoteIovec, len(batch))
			for i, r := range batch {
				remote[i] = unix.RemoteIovec{Base: uintptr(r.Start), Len: int(r.Size)}
			}
			buf := make([]byte, batchBytes)
			local := []unix.Iovec{{Base: &buf[0], Len: uint64(batchBytes)}}

			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}

				n, err := unix.ProcessVMReadv(pid, local, remote, 0)
				if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
					continue
				}
				if errors.Is(err, unix.ENOMEM) {
					time.Sleep(oomMinBackoff + time.Duration(rand.Intn(int(oomMaxJitter.Milliseconds())))*time.Millisecond)

					continue
				}
				if err != nil {
					return fmt.Errorf("failed to read memory: %w", err)
				}
				if int64(n) != batchBytes {
					return fmt.Errorf("failed to read memory: expected %d bytes, got %d", batchBytes, n)
				}

				if err := writeFullAt(c.file, buf, off); err != nil {
					return fmt.Errorf("failed to write memory cache: %w", err)
				}
				c.setIsCached(off, batchBytes)

				return nil
			}
		})
}

// Split ranges so there are no ranges larger than maxSize.
// This is not an optimal split—ideally we would split the ranges so that we can fill each call to unix.ProcessVMReadv to the max size.
// This is though a very simple split that will work and the syscalls overhead here is not very high as opposed to the other things.
func splitOversizedRanges(ranges []Range, maxSize int64) (result []Range) {
	for _, r := range ranges {
		if r.Size <= maxSize {
			result = append(result, r)

			continue
		}

		for offset := int64(0); offset < r.Size; offset += maxSize {
			result = append(result, Range{
				Start: r.Start + offset,
				Size:  min(r.Size-offset, maxSize),
			})
		}
	}

	return result
}

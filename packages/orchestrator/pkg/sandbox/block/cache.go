package block

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/edsrzf/mmap-go"
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
	mmap      *mmap.MMap
	mu        sync.RWMutex
	tracker   *Tracker // Dirty: payload in mmap; Zero: punched, emitted as Empty in the diff
	dirtyFile bool
	closed    atomic.Bool
}

func NewCache(size, blockSize int64, filePath string, dirtyFile bool) (*Cache, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	defer f.Close()

	if size == 0 {
		return &Cache{
			filePath:  filePath,
			size:      size,
			blockSize: blockSize,
			dirtyFile: dirtyFile,
			tracker:   NewTracker(),
		}, nil
	}

	// This should create a sparse file on Linux.
	err = f.Truncate(size)
	if err != nil {
		return nil, fmt.Errorf("error allocating file: %w", err)
	}

	if size > math.MaxInt {
		return nil, fmt.Errorf("size too big: %d > %d", size, math.MaxInt)
	}

	mm, err := mmap.MapRegion(f, int(size), mmap.RDWR, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	return &Cache{
		mmap:      &mm,
		filePath:  filePath,
		size:      size,
		blockSize: blockSize,
		dirtyFile: dirtyFile,
		tracker:   NewTracker(),
	}, nil
}

func (c *Cache) isClosed() bool {
	return c.closed.Load()
}

func (c *Cache) ExportToDiff(ctx context.Context, out *os.File) (*header.DiffMetadata, error) {
	ctx, childSpan := tracer.Start(ctx, "export-to-diff")
	defer childSpan.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return nil, NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return header.NewDiffMetadata(c.blockSize, nil, nil), nil
	}

	f, err := os.Open(c.filePath)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer f.Close()

	src := int(f.Fd())

	// Explicit mmap flush is not necessary, because the kernel will handle that as part of the copy_file_range syscall.
	// Calling sync_file_range marks the range for writeback and starts it early.
	// This is just an optimization, so if it fails just log a warning and let copy_file_range do the actual work.
	err = unix.SyncFileRange(src, 0, c.size, unix.SYNC_FILE_RANGE_WRITE)
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
				sr := io.NewSectionReader(f, readOffset, int64(remaining))
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

// Dedup compares its contents against the original memory file and creates
// a new Cache
func (c *Cache) Dedup(
	ctx context.Context,
	originalMemfile ReadonlyDevice,
	dirtyBlocks *roaring.Bitmap,
	blockSize int64,
	totalMemorySize int64,
	outPath string,
) (*Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "dedup-cache")
	defer span.End()

	pageDirty := roaring.New()

	srcBuf := make([]byte, blockSize)
	origBuf := make([]byte, blockSize)

	// First pass: count unique pages to size the output cache
	var cacheOffset int64
	var totalPages, dedupedPages uint
	var uniquePageCount int64

	for r := range BitsetRanges(dirtyBlocks, blockSize) {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		// Loop within the Range at blockSize granularity to avoid creating (temporarily)
		// a duplicate of the dirty contents of the cache within orchestrator memory.
		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			_, err := c.ReadAt(srcBuf, cacheOffset)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read exported cache at offset %d: %w", cacheOffset, err)
			}

			_, err = originalMemfile.ReadAt(ctx, origBuf, r.Start+chunkOff)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to read from original memfile at offset %d: %w", r.Start+chunkOff, err)
			}

			for i := int64(0); i < blockSize; i += header.PageSize {
				totalPages++
				if bytes.Equal(srcBuf[i:i+header.PageSize], origBuf[i:i+header.PageSize]) {
					dedupedPages++

					continue
				}

				pageIdx := uint32((r.Start + chunkOff + i) / header.PageSize)
				pageDirty.Add(pageIdx)
				uniquePageCount++
			}

			cacheOffset += blockSize
		}
	}

	dedupSize := uniquePageCount * header.PageSize
	dedupCache, err := NewCache(dedupSize, header.PageSize, outPath, false)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create dedup cache: %w", err)
	}

	cacheOffset = 0
	var writeOffset int64

	for r := range BitsetRanges(dirtyBlocks, blockSize) {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}

		for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
			_, err := c.ReadAt(srcBuf, cacheOffset)
			if err != nil {
				return nil, nil, errors.Join(fmt.Errorf("failed to read exported cache at offset %d: %w", cacheOffset, err), dedupCache.Close())
			}

			for i := int64(0); i < blockSize; i += header.PageSize {
				pageIdx := uint32((r.Start + chunkOff + i) / header.PageSize)
				if !pageDirty.Contains(pageIdx) {
					continue
				}
				_, err = dedupCache.WriteAt(srcBuf[i:i+header.PageSize], writeOffset)
				if err != nil {
					return nil, nil, errors.Join(fmt.Errorf("failed to write deduped cache at offset %d: %w", writeOffset, err), dedupCache.Close())
				}

				writeOffset += header.PageSize
			}

			cacheOffset += blockSize
		}
	}

	exportedSize := int64(dirtyBlocks.GetCardinality()) * blockSize

	telemetry.SetAttributes(ctx,
		attribute.Int64("dedup.total_pages", int64(totalPages)),
		attribute.Int64("dedup.deduped_pages", int64(dedupedPages)),
		attribute.Int64("dedup.unique_pages", uniquePageCount),
		attribute.Float64("dedup.ratio", safeDivide(float64(dedupedPages), float64(totalPages))),
	)

	logger.L().Info(ctx, "4KiB page dedup completed",
		zap.Uint64("dirty_blocks", dirtyBlocks.GetCardinality()),
		zap.Uint("total_4k_pages", totalPages),
		zap.Uint("deduped_pages", dedupedPages),
		zap.Int64("unique_pages", uniquePageCount),
		zap.Int64("exported_size_bytes", exportedSize),
		zap.Int64("dedup_size_bytes", dedupSize),
		zap.String("reduction", fmt.Sprintf("%.1f%%", safeDivide(float64(dedupedPages), float64(totalPages))*100)),
	)

	// Every page NOT in pageDirty must be mapped to uuid.Nil (zeros)
	pageEmpty := roaring.New()
	totalPageCount := uint64(header.TotalBlocks(totalMemorySize, header.PageSize))
	pageEmpty.Flip(0, totalPageCount)
	pageEmpty.AndNot(pageDirty)

	return dedupCache, &header.DiffMetadata{
		Dirty:     pageDirty,
		Empty:     pageEmpty,
		BlockSize: header.PageSize,
	}, nil
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}

	return a / b
}

func (c *Cache) ReadAt(b []byte, off int64) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.mmap == nil {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	slice, err := c.Slice(off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("error slicing mmap: %w", err)
	}

	return copy(b, slice), nil
}

func (c *Cache) WriteAt(b []byte, off int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mmap == nil {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	return c.WriteAtWithoutLock(b, off)
}

func (c *Cache) Close() (e error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mmap == nil {
		return os.RemoveAll(c.filePath)
	}

	succ := c.closed.CompareAndSwap(false, true)
	if !succ {
		return NewErrCacheClosed(c.filePath)
	}

	err := c.mmap.Unmap()
	if err != nil {
		e = errors.Join(e, fmt.Errorf("error unmapping mmap: %w", err))
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

// Slice returns a slice of the mmap.
// When using Slice you must ensure thread safety, ideally by only writing to the same block once and the exposing the slice.
func (c *Cache) Slice(off, length int64) ([]byte, error) {
	if c.isClosed() {
		return nil, NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return nil, nil
	}

	if c.dirtyFile || c.isCached(off, length) {
		end := min(off+length, c.size)

		return (*c.mmap)[off:end], nil
	}

	return nil, BytesNotAvailableError{}
}

// sliceDirect returns a slice of the mmap without checking isCached.
// Used by the streaming chunker after the waiter mechanism has confirmed data availability.
func (c *Cache) sliceDirect(off, length int64) ([]byte, error) {
	if c.isClosed() {
		return nil, NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return nil, nil
	}

	if off < 0 || off >= c.size {
		return nil, BytesNotAvailableError{}
	}

	end := min(off+length, c.size)

	return (*c.mmap)[off:end], nil
}

// Zero blocks are treated as cached: the mmap region reads back as zero (punched).
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

// punchHole frees backing pages; clear() fallback if MADV_REMOVE is unsupported.
func (c *Cache) punchHole(off, length int64) {
	if err := unix.Madvise((*c.mmap)[off:off+length], unix.MADV_REMOVE); err != nil {
		clear((*c.mmap)[off : off+length])
	}
}

// When using WriteAtWithoutLock you must ensure thread safety, ideally by only writing to the same block once and the exposing the slice.
func (c *Cache) WriteAtWithoutLock(b []byte, off int64) (int, error) {
	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return 0, nil
	}

	end := min(off+int64(len(b)), c.size)
	if end <= off {
		return 0, nil
	}

	// detect-zeroes=unmap: coalesce contiguous same-state blocks into one bulk
	// copy or punchHole call. Caller must pass a block-aligned write (NBD invariant).
	flush := func(runStart, runEnd int64, runZero bool) {
		startIdx := uint32(header.BlockIdx(runStart, c.blockSize))
		endIdx := uint32(header.BlockCeilIdx(runEnd, c.blockSize))
		if runZero {
			c.punchHole(runStart, runEnd-runStart)
			c.tracker.SetRange(startIdx, endIdx, Zero)
		} else {
			copy((*c.mmap)[runStart:runEnd], b[runStart-off:runEnd-off])
			c.tracker.SetRange(startIdx, endIdx, Dirty)
		}
	}

	runStart := off
	runZero := header.IsZero(b[:c.blockSize])
	for i := off + c.blockSize; i < end; i += c.blockSize {
		z := header.IsZero(b[i-off : i-off+c.blockSize])
		if z == runZero {
			continue
		}
		flush(runStart, i, runZero)
		runStart = i
		runZero = z
	}
	flush(runStart, end, runZero)

	return int(end - off), nil
}

// WriteZeroesAt punches the range and marks all touched blocks Zero.
// Caller must pass a block-aligned offset/length (NBD invariant).
func (c *Cache) WriteZeroesAt(off, length int64) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.mmap == nil {
		return 0, nil
	}

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	end := min(off+length, c.size)
	if end <= off {
		return 0, nil
	}

	c.punchHole(off, end-off)
	c.tracker.SetRange(
		uint32(header.BlockIdx(off, c.blockSize)),
		uint32(header.BlockCeilIdx(end, c.blockSize)),
		Zero,
	)

	return int(end - off), nil
}

// FileSize returns the size of the cache on disk.
// The size might differ from the dirty size, as it may not be fully on disk.
func (c *Cache) FileSize() (int64, error) {
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

func (c *Cache) address(off int64) (*byte, error) {
	if c.mmap == nil {
		return nil, nil
	}

	if off >= c.size {
		return nil, fmt.Errorf("offset %d is out of bounds", off)
	}

	return &(*c.mmap)[off], nil
}

// addressBytes returns a slice of the mmap and a function to release the read lock which blocks the cache from being closed.
func (c *Cache) addressBytes(off, length int64) ([]byte, func(), error) {
	c.mu.RLock()

	if c.mmap == nil {
		c.mu.RUnlock()

		return nil, func() {}, nil
	}

	if c.isClosed() {
		c.mu.RUnlock()

		return nil, func() {}, NewErrCacheClosed(c.filePath)
	}

	if off >= c.size {
		c.mu.RUnlock()

		return nil, func() {}, fmt.Errorf("offset %d is out of bounds", off)
	}

	releaseCacheCloseLock := func() {
		c.mu.RUnlock()
	}

	end := min(off+length, c.size)

	return (*c.mmap)[off:end], releaseCacheCloseLock, nil
}

func (c *Cache) BlockSize() int64 {
	return c.blockSize
}

func (c *Cache) Path() string {
	return c.filePath
}

func NewCacheFromMemfd(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	ranges []Range,
) (*Cache, error) {
	size := GetSize(ranges)

	logger.L().Debug(ctx, "Creating cache from memfd")

	cache, err := NewCache(size, blockSize, filePath, false)
	if err != nil {
		return nil, err
	}

	if size == 0 {
		return cache, nil
	}

	err = cache.copyFromMemfd(ctx, memfd, ranges)
	if err != nil {
		return nil, fmt.Errorf("failed to copy from memfd: %w", errors.Join(err, cache.Close()))
	}

	return cache, nil
}

func (c *Cache) copyFromMemfd(ctx context.Context, memfd *Memfd, ranges []Range) error {
	var cacheOff int64

	for _, r := range ranges {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		srcOff := r.Start
		remaining := r.Size
		rangeStart := cacheOff

		for remaining > 0 {
			n, err := unix.Pread(memfd.memfd, (*c.mmap)[cacheOff:cacheOff+remaining], srcOff)
			if errors.Is(err, syscall.EINTR) {
				continue
			}

			if err != nil {
				return fmt.Errorf("pread from memfd failed: %w", err)
			}

			if n == 0 {
				return fmt.Errorf("pread from memfd: EOF with %d bytes remaining", remaining)
			}

			srcOff += int64(n)
			cacheOff += int64(n)
			remaining -= int64(n)
		}

		c.setIsCached(rangeStart, r.Size)
	}

	return nil
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
	// We need to align the maximum read/write count to the block size, so we can use mark the offsets as dirty correctly.
	// Because the MAX_RW_COUNT is not aligned to arbitrary block sizes, we need to align it to the block size we use for the cache.
	alignedRwCount := getAlignedMaxRwCount(c.blockSize)

	// We need to split the ranges because the Kernel does not support reading/writing more than MAX_RW_COUNT bytes in a single operation.
	ranges := splitOversizedRanges(rs, alignedRwCount)

	var offset int64
	var rangeIdx int64

	for {
		var remote []unix.RemoteIovec

		var segmentSize int64

		// We iterate over the range of all ranges until we have reached the limit of the IOV_MAX,
		// or until the next range would overflow the MAX_RW_COUNT.
		for ; rangeIdx < int64(len(ranges)); rangeIdx++ {
			r := ranges[rangeIdx]

			if len(remote) == IOV_MAX {
				break
			}

			if segmentSize+r.Size > alignedRwCount {
				break
			}

			remote = append(remote, unix.RemoteIovec{
				Base: uintptr(r.Start),
				Len:  int(r.Size),
			})

			segmentSize += r.Size
		}

		if len(remote) == 0 {
			break
		}

		address, err := c.address(offset)
		if err != nil {
			return fmt.Errorf("failed to get address: %w", err)
		}

		local := []unix.Iovec{
			{
				Base: address,
				// We could keep this as full cache length, but we might as well be exact here.
				Len: uint64(segmentSize),
			},
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			// We could retry only on the remaining segment size, but for simplicity we retry the whole segment.
			n, err := unix.ProcessVMReadv(pid,
				local,
				remote,
				0,
			)
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENOMEM) {
				time.Sleep(oomMinBackoff + time.Duration(rand.Intn(int(oomMaxJitter.Milliseconds())))*time.Millisecond)

				continue
			}

			if err != nil {
				return fmt.Errorf("failed to read memory: %w", err)
			}

			if int64(n) != segmentSize {
				return fmt.Errorf("failed to read memory: expected %d bytes, got %d", segmentSize, n)
			}

			c.setIsCached(offset, segmentSize)

			offset += segmentSize

			break
		}
	}

	return nil
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

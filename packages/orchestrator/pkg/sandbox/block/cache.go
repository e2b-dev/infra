package block

import (
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

// blockState classifies cached blocks for diff export and read fall-through.
//   - blockUntouched: no guest write; reads fall through to the template.
//   - blockDirty:     guest wrote bytes; copied verbatim into the diff.
//   - blockZero:      guest wrote zeros (or trimmed); recorded in Empty so the
//     diff carries no payload bytes for it.
type blockState uint8

const (
	blockUntouched blockState = iota
	blockDirty
	blockZero
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
	state     *StateTracker[blockState]
	dirtyFile bool
	closed    atomic.Bool
}

func NewCache(size, blockSize int64, filePath string, dirtyFile bool) (*Cache, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}

	defer f.Close()

	state, err := NewStateTracker(blockUntouched, blockDirty, blockZero)
	if err != nil {
		return nil, fmt.Errorf("error creating state tracker: %w", err)
	}

	if size == 0 {
		return &Cache{
			filePath:  filePath,
			size:      size,
			blockSize: blockSize,
			dirtyFile: dirtyFile,
			state:     state,
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
		state:     state,
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
		return header.NewDiffMetadata(c.blockSize, roaring.New()), nil
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

	dirty, empty := c.state.Export()
	diffMetadata := header.NewDiffMetadata(c.blockSize, dirty)
	diffMetadata.Empty = empty

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

func (c *Cache) isCached(off, length int64) bool {
	start := uint64(header.BlockIdx(off, c.blockSize))
	end := uint64(header.BlockCeilIdx(min(off+length, c.size), c.blockSize))

	return c.state.HasRange(start, end)
}

// setIsCached marks the byte range as resident in the cache (blockDirty),
// signalling that subsequent reads must be served from the mmap rather than
// fetched from the template.
func (c *Cache) setIsCached(off, length int64) {
	c.markBlocks(off, length, blockDirty)
}

func (c *Cache) markBlocks(off, length int64, s blockState) {
	start := uint64(header.BlockIdx(off, c.blockSize))
	end := uint64(header.BlockCeilIdx(off+length, c.blockSize))
	if err := c.state.SetRange(start, end, s); err != nil {
		// Should be unreachable: blockUntouched/Dirty/Zero are wired in NewCache.
		panic(fmt.Errorf("cache: state.SetRange %d-%d: %w", start, end, err))
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

	n := copy((*c.mmap)[off:end], b)

	c.setIsCached(off, end-off)

	return n, nil
}

// WriteZeroesAt records that [off, off+length) reads as zero without writing
// the zero bytes to the diff. Only block-aligned, fully-covered blocks are
// marked Empty: any sub-block tail must be zeroed in-place and tracked as
// dirty to preserve correctness against partial overwrites.
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

	alignedStart := alignUp(off, c.blockSize)
	alignedEnd := alignDown(end, c.blockSize)

	// Aligned core: clear mmap (so reads return zero even after a prior dirty
	// write to the same block) and mark blocks as Empty in the diff.
	if alignedStart < alignedEnd {
		clear((*c.mmap)[alignedStart:alignedEnd])
		c.markBlocks(alignedStart, alignedEnd-alignedStart, blockZero)
	}

	// Unaligned head/tail share their block with neighbouring data, so we
	// can't claim the whole block as Empty — write zeros and mark Dirty.
	if off < alignedStart {
		head := min(alignedStart, end)
		clear((*c.mmap)[off:head])
		c.markBlocks(off, head-off, blockDirty)
	}
	if alignedEnd < end && alignedEnd >= alignedStart {
		tail := max(alignedEnd, off)
		clear((*c.mmap)[tail:end])
		c.markBlocks(tail, end-tail, blockDirty)
	}

	return int(end - off), nil
}

func alignUp(v, align int64) int64   { return (v + align - 1) / align * align }
func alignDown(v, align int64) int64 { return v / align * align }

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

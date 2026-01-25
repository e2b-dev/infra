package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/edsrzf/mmap-go"
	"go.opentelemetry.io/otel"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	oomMinBackoff = 100 * time.Millisecond
	oomMaxJitter  = 100 * time.Millisecond
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block")

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
	dirty     sync.Map
	dirtyFile bool
	closed    atomic.Bool
}

// When we are passing filePath that is a file that has content we want to server want to use dirtyFile = true.
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

	mm, err := mmap.MapRegion(f, int(size), unix.PROT_READ|unix.PROT_WRITE, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("error mapping file: %w", err)
	}

	return &Cache{
		mmap:      &mm,
		filePath:  filePath,
		size:      size,
		blockSize: blockSize,
		dirtyFile: dirtyFile,
	}, nil
}

func (c *Cache) isClosed() bool {
	return c.closed.Load()
}

func (c *Cache) Sync() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return nil
	}

	err := c.mmap.Flush()
	if err != nil {
		return fmt.Errorf("error syncing cache: %w", err)
	}

	return nil
}

func (c *Cache) ExportToDiff(ctx context.Context, out io.Writer) (*header.DiffMetadata, error) {
	ctx, childSpan := tracer.Start(ctx, "export-to-diff")
	defer childSpan.End()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.isClosed() {
		return nil, NewErrCacheClosed(c.filePath)
	}

	if c.mmap == nil {
		return &header.DiffMetadata{
			Dirty:     bitset.New(0),
			Empty:     bitset.New(0),
			BlockSize: c.blockSize,
		}, nil
	}

	err := c.mmap.Flush()
	if err != nil {
		return nil, fmt.Errorf("error flushing mmap: %w", err)
	}

	builder := header.NewDiffMetadataBuilder(c.size, c.blockSize)

	for _, offset := range c.dirtySortedKeys() {
		block := (*c.mmap)[offset : offset+c.blockSize]

		err := builder.Process(ctx, block, out, offset)
		if err != nil {
			return nil, fmt.Errorf("error processing block %d: %w", offset, err)
		}
	}

	return builder.Build(), nil
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

func (c *Cache) isCached(off, length int64) bool {
	for _, blockOff := range header.BlocksOffsets(length, c.blockSize) {
		_, dirty := c.dirty.Load(off + blockOff)
		if !dirty {
			return false
		}
	}

	return true
}

func (c *Cache) setIsCached(off, length int64) {
	for _, blockOff := range header.BlocksOffsets(length, c.blockSize) {
		c.dirty.Store(off+blockOff, struct{}{})
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

// dirtySortedKeys returns a sorted list of dirty keys.
// Key represents a block offset.
func (c *Cache) dirtySortedKeys() []int64 {
	var keys []int64
	c.dirty.Range(func(key, _ any) bool {
		keys = append(keys, key.(int64))

		return true
	})
	slices.Sort(keys)

	return keys
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

			for _, blockOff := range header.BlocksOffsets(segmentSize, c.blockSize) {
				c.dirty.Store(offset+blockOff, struct{}{})
			}

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

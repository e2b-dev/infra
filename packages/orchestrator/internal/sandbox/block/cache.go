package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/edsrzf/mmap-go"
	"github.com/tklauser/go-sysconf"
	"go.opentelemetry.io/otel"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	oomMinBackoff = 100 * time.Millisecond
	oomMaxJitter  = 100 * time.Millisecond
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block")

// IOV_MAX is the limit of the vectors that can be passed in a single ioctl call.
var IOV_MAX = utils.Must(getIOVMax())

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

	if c.isClosed() {
		return 0, NewErrCacheClosed(c.filePath)
	}

	return c.WriteAtWithoutLock(b, off)
}

func (c *Cache) Close() (e error) {
	c.mu.Lock()
	defer c.mu.Unlock()

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

	if c.dirtyFile || c.isCached(off, length) {
		end := off + length
		if end > c.size {
			end = c.size
		}

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

	end := off + int64(len(b))
	if end > c.size {
		end = c.size
	}

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
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})

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

func (c *Cache) Address(off uint64) *byte {
	return &(*c.mmap)[off]
}

func (c *Cache) BlockSize() int64 {
	return c.blockSize
}

func (c *Cache) Path() string {
	return c.filePath
}

func (c *Cache) CopyFromProcess(
	ctx context.Context,
	pid int,
	ranges []Range,
) error {
	var start uint64

	for i := 0; i < len(ranges); i += int(IOV_MAX) {
		segmentRanges := ranges[i:min(i+int(IOV_MAX), len(ranges))]

		remote := make([]unix.RemoteIovec, len(segmentRanges))

		var segmentSize uint64

		for j, r := range segmentRanges {
			remote[j] = unix.RemoteIovec{
				Base: uintptr(r.Start),
				Len:  int(r.Size),
			}

			segmentSize += r.Size
		}

		local := []unix.Iovec{
			{
				Base: c.Address(start),
				// We could keep this as full cache length, but we might as well be exact here.
				Len: segmentSize,
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

			if uint64(n) != segmentSize {
				return fmt.Errorf("failed to read memory: expected %d bytes, got %d", segmentSize, n)
			}

			start += segmentSize

			break
		}
	}

	return nil
}

func getIOVMax() (int64, error) {
	iovMax, err := sysconf.Sysconf(sysconf.SC_IOV_MAX)
	if err != nil {
		return 0, fmt.Errorf("failed to get IOV_MAX: %w", err)
	}

	return iovMax, nil
}

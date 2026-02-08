package block

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sync/singleflight"
	"golang.org/x/sys/unix"
)

// MMapFrameCache is a compressed frame mmap cache that tracks presence at frame
// granularity (by offset), eliminating the block-alignment issues of Cache.
//
// It provides a GetOrFetch API that atomically checks the cache and populates
// it on miss, with singleflight deduplication for concurrent fetches.
type MMapFrameCache struct {
	filePath string
	size     int64
	mmap     *mmap.MMap
	mu       sync.RWMutex
	closed   atomic.Bool
	frames   sync.Map           // map[int64]struct{} - tracks cached frames by offset
	fetchers singleflight.Group // dedup concurrent fetches for same offset
}

// NewFrameCache creates a new FrameCache backed by a sparse mmap file.
func NewFrameCache(size int64, filePath string) (*MMapFrameCache, error) {
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("error opening file: %w", err)
	}
	defer f.Close()

	if size == 0 {
		return &MMapFrameCache{
			filePath: filePath,
			size:     size,
		}, nil
	}

	// Create a sparse file.
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

	return &MMapFrameCache{
		mmap:     &mm,
		filePath: filePath,
		size:     size,
	}, nil
}

// GetOrFetch returns frame data from the cache at the given offset and length.
// If the frame is not cached, fetchFn is called with a writable mmap buffer to populate it.
// Concurrent fetches for the same offset are deduplicated via singleflight.
// Returns (data, wasCacheHit, error).
func (fc *MMapFrameCache) GetOrFetch(off, length int64, fetchFn func(buf []byte) error) ([]byte, bool, error) {
	// Fast path: already cached
	if _, cached := fc.frames.Load(off); cached {
		fc.mu.RLock()
		defer fc.mu.RUnlock()

		if fc.closed.Load() {
			return nil, false, NewErrCacheClosed(fc.filePath)
		}

		if fc.mmap == nil {
			return nil, false, nil
		}

		end := min(off+length, fc.size)

		return (*fc.mmap)[off:end], true, nil
	}

	// Slow path: fetch with singleflight dedup
	key := strconv.FormatInt(off, 10)
	_, err, _ := fc.fetchers.Do(key, func() (any, error) {
		// Double-check after acquiring slot
		if _, cached := fc.frames.Load(off); cached {
			return nil, nil
		}

		return nil, fc.fetch(off, length, fetchFn)
	})
	if err != nil {
		return nil, false, err
	}

	// Read from mmap after fetch
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.closed.Load() {
		return nil, false, NewErrCacheClosed(fc.filePath)
	}

	if fc.mmap == nil {
		return nil, false, nil
	}

	end := min(off+length, fc.size)

	return (*fc.mmap)[off:end], false, nil
}

// fetch writes data into the mmap and marks the frame as cached.
func (fc *MMapFrameCache) fetch(off, length int64, fetchFn func(buf []byte) error) error {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	if fc.closed.Load() {
		return NewErrCacheClosed(fc.filePath)
	}

	if fc.mmap == nil {
		return nil
	}

	end := min(off+length, fc.size)
	buf := (*fc.mmap)[off:end]

	if err := fetchFn(buf); err != nil {
		return err
	}

	fc.frames.Store(off, struct{}{})

	return nil
}

// Close unmaps the file and removes the backing file.
func (fc *MMapFrameCache) Close() (e error) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if fc.mmap == nil {
		return os.RemoveAll(fc.filePath)
	}

	succ := fc.closed.CompareAndSwap(false, true)
	if !succ {
		return NewErrCacheClosed(fc.filePath)
	}

	err := fc.mmap.Unmap()
	if err != nil {
		e = errors.Join(e, fmt.Errorf("error unmapping mmap: %w", err))
	}

	e = errors.Join(e, os.RemoveAll(fc.filePath))

	return e
}

// FileSize returns the on-disk size of the cache file (sparse allocation).
func (fc *MMapFrameCache) FileSize() (int64, error) {
	var stat syscall.Stat_t
	err := syscall.Stat(fc.filePath, &stat)
	if err != nil {
		return 0, fmt.Errorf("failed to get file stats: %w", err)
	}

	var fsStat syscall.Statfs_t
	err = syscall.Statfs(fc.filePath, &fsStat)
	if err != nil {
		return 0, fmt.Errorf("failed to get disk stats for path %s: %w", fc.filePath, err)
	}

	return stat.Blocks * fsStat.Bsize, nil
}

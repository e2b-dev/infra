//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/RoaringBitmap/roaring/v2"
	"golang.org/x/sys/unix"
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
	cache, err := NewCache(int64(dirty.GetCardinality())*blockSize, blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}

	var cacheOff int64
	for r := range BitsetRanges(dirty, blockSize) {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, memfd.Close(), cache.Close())
		}

		src, err := memfd.Slice(r.Start, r.Size)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("memfd slice [%d,%d): %w", r.Start, r.Start+r.Size, err), memfd.Close(), cache.Close())
		}

		copy((*cache.mmap)[cacheOff:cacheOff+r.Size], src)
		cache.setIsCached(cacheOff, r.Size)
		cacheOff += r.Size
	}

	if err := memfd.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close memfd: %w", err), cache.Close())
	}

	return cache, nil
}

// MemfdCache wraps a Cache being populated from a memfd on a background
// goroutine. Reads route to the cache file: if the requested range is
// already populated (by runCopy or a prior demand-page), they're served
// zero-copy; otherwise the read demand-pages from memfd → cache + marks
// the range cached, so runCopy can skip it later. The memfd is closed and
// its hugetlb pages released as soon as the full copy is done. Slices
// returned have the same ownership as *Cache.Slice (valid until Close).
//
// Designed for the resume-from-just-paused-snapshot case: another sandbox
// can resume and read this diff without waiting for the upload to finish.
type MemfdCache struct {
	*Cache

	// mu guards src + cache writes by runCopy/demand-page paths.
	mu  sync.Mutex
	src *memfdSource // nil after the background copy completes

	cancel context.CancelFunc
	done   chan struct{} // closed by runCopy; serves as happens-before for err
	err    error
}

// NewCacheFromMemfdAsync starts the memfd → cache copy on a goroutine so
// Pause can return as soon as snapshot file + diff metadata are written.
// The returned wrapper takes ownership of memfd; Close cancels the copy.
func NewCacheFromMemfdAsync(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*MemfdCache, error) {
	cache, err := NewCache(int64(dirty.GetCardinality())*blockSize, blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}
	if dirty.IsEmpty() {
		if closeErr := memfd.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close memfd: %w", closeErr), cache.Close())
		}

		return &MemfdCache{Cache: cache}, nil
	}

	copyCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m := &MemfdCache{
		Cache:  cache,
		src:    newMemfdSource(memfd, dirty, blockSize),
		cancel: cancel,
		done:   make(chan struct{}),
	}

	go m.runCopy(copyCtx, dirty, blockSize)

	return m, nil
}

func (m *MemfdCache) runCopy(ctx context.Context, dirty *roaring.Bitmap, blockSize int64) {
	var err error
	var cacheOff int64
	for r := range BitsetRanges(dirty, blockSize) {
		if err = ctx.Err(); err != nil {
			break
		}
		if err = m.copyRange(cacheOff, r); err != nil {
			break
		}
		cacheOff += r.Size
	}

	m.mu.Lock()
	memfd := m.src.memfd
	m.src = nil
	m.mu.Unlock()

	if closeErr := memfd.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close memfd: %w", closeErr))
	}
	m.err = err
	close(m.done)
}

// copyRange copies a single source range memfd → cache under the lock,
// skipping it if a concurrent demand-page already populated those bytes.
func (m *MemfdCache) copyRange(cacheOff int64, r Range) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Cache.isCached(cacheOff, r.Size) {
		return nil
	}

	src, err := m.src.memfd.Slice(r.Start, r.Size)
	if err != nil {
		return fmt.Errorf("memfd slice [%d,%d): %w", r.Start, r.Start+r.Size, err)
	}
	copy((*m.Cache.mmap)[cacheOff:cacheOff+r.Size], src)
	m.Cache.setIsCached(cacheOff, r.Size)

	return nil
}

// Wait blocks until the background copy completes. Only needed by callers
// reading the cache file directly (e.g. via CachePath); ReadAt and Slice
// work without waiting.
func (m *MemfdCache) Wait(ctx context.Context) error {
	if m.done == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.done:
	}

	return m.err
}

func (m *MemfdCache) ReadAt(b []byte, off int64) (int, error) {
	if err := m.ensureCached(off, int64(len(b))); err != nil {
		return 0, err
	}

	return m.Cache.ReadAt(b, off)
}

// Slice returns the embedded Cache's zero-copy slice — demand-paging the
// range from memfd if runCopy hasn't reached it yet. Ownership matches
// *Cache.Slice: valid until Close.
func (m *MemfdCache) Slice(off, length int64) ([]byte, error) {
	if err := m.ensureCached(off, length); err != nil {
		return nil, err
	}

	return m.Cache.Slice(off, length)
}

// ensureCached makes sure [off, off+length) is in the cache file, copying
// from memfd if necessary. After it returns, Cache.ReadAt/Slice operate on
// populated bytes.
func (m *MemfdCache) ensureCached(off, length int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.src == nil || m.Cache.isCached(off, length) {
		return nil
	}

	return m.src.fill(m.Cache, off, length)
}

func (m *MemfdCache) Close() error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}

	return m.Cache.Close()
}

// memfdSource indexes memfd-backed ranges by cache offset so reads can
// resolve a (cacheOff, length) into the corresponding memfd bytes.
type memfdSource struct {
	memfd   *Memfd
	entries []memfdRange
}

type memfdRange struct{ cacheStart, srcStart, size int64 }

func newMemfdSource(memfd *Memfd, dirty *roaring.Bitmap, blockSize int64) *memfdSource {
	var entries []memfdRange
	var cacheOff int64
	for r := range BitsetRanges(dirty, blockSize) {
		entries = append(entries, memfdRange{cacheStart: cacheOff, srcStart: r.Start, size: r.Size})
		cacheOff += r.Size
	}

	return &memfdSource{memfd: memfd, entries: entries}
}

// fill copies [cacheOff, cacheOff+length) from memfd into cache.mmap and
// marks the range cached. Caller must hold the cache lock.
func (s *memfdSource) fill(cache *Cache, cacheOff, length int64) error {
	end := cacheOff + length
	for cacheOff < end {
		i := s.find(cacheOff)
		if i < 0 {
			return BytesNotAvailableError{}
		}
		e := s.entries[i]
		offsetInEntry := cacheOff - e.cacheStart
		toCopy := min(end-cacheOff, e.size-offsetInEntry)

		src, err := s.memfd.Slice(e.srcStart+offsetInEntry, toCopy)
		if err != nil {
			return fmt.Errorf("memfd slice: %w", err)
		}
		copy((*cache.mmap)[cacheOff:cacheOff+toCopy], src)
		cache.setIsCached(cacheOff, toCopy)

		cacheOff += toCopy
	}

	return nil
}

// find returns the index of the entry containing cacheOff, or -1.
func (s *memfdSource) find(cacheOff int64) int {
	lo, hi := 0, len(s.entries)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.entries[mid].cacheStart > cacheOff {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	i := lo - 1
	if i < 0 || cacheOff >= s.entries[i].cacheStart+s.entries[i].size {
		return -1
	}

	return i
}

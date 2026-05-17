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

// MemfdCache wraps a Cache that is populated from a memfd on a background
// goroutine. While the copy is in flight reads route to the memfd directly
// (zero-copy via the mmap), so in-flight consumers — e.g. a follow-up
// sandbox that resumes from a just-paused snapshot before its upload
// finishes — see correct bytes without blocking. Once the copy completes,
// runCopy closes the memfd (releasing the hugetlb pages — potentially
// tens of GB) and subsequent reads delegate to the embedded Cache.
//
// The cache file is the side-effect runCopy writes; use Wait (or
// CachePath's Wait type-assertion) only if you need the file path.
type MemfdCache struct {
	*Cache

	// mu guards src across the runCopy → reader transition: runCopy nils
	// src and closes memfd under Lock; readers check src under RLock so
	// they never read from a closed/munmap'd memfd.
	mu  sync.RWMutex
	src *memfdSource // non-nil while the background copy is in flight

	cancel context.CancelFunc
	done   chan struct{} // closed by runCopy; serves as the happens-before for err
	err    error
}

// NewCacheFromMemfdAsync starts the memfd→cache copy on a goroutine so gRPC
// Pause can return as soon as the snapshot file and diff metadata are
// written. The returned wrapper takes ownership of memfd; runCopy closes it
// when the copy completes (or is cancelled via Close).
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

	// Detach from the request context so the copy can outlive Pause; Close
	// drives cancellation.
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
	err := copyFromMemfd(ctx, m.Cache, m.src.memfd, dirty, blockSize)

	// Park src=nil under the lock so any concurrent reader has either
	// finished its memfd access (we waited for their RLock) or will take
	// the cache path on its next call.
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

// Wait blocks until the background copy completes (or ctx is cancelled).
// Only needed if the caller will read the cache file directly (e.g. via
// CachePath); ReadAt and Slice work without waiting.
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.src != nil {
		return m.src.readAt(b, off)
	}

	return m.Cache.ReadAt(b, off)
}

// Slice copies bytes into a fresh buffer while the copy is in flight
// (a zero-copy memfd view would dangle once runCopy munmaps), and
// returns the embedded Cache's zero-copy slice afterwards.
func (m *MemfdCache) Slice(off, length int64) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.src != nil {
		b := make([]byte, length)
		n, err := m.src.readAt(b, off)
		if err != nil {
			return nil, err
		}
		if int64(n) < length {
			return nil, BytesNotAvailableError{}
		}

		return b, nil
	}

	return m.Cache.Slice(off, length)
}

func (m *MemfdCache) Close() error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}

	return m.Cache.Close()
}

// memfdSource indexes the memfd-backed ranges by cache offset so reads can
// be served from the memfd directly without touching the in-flight cache.
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

// findEntry returns the index of the entry containing cacheOff, or -1.
func (s *memfdSource) findEntry(cacheOff int64) int {
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

func (s *memfdSource) readAt(b []byte, cacheOff int64) (int, error) {
	n := 0
	for n < len(b) {
		i := s.findEntry(cacheOff + int64(n))
		if i < 0 {
			return n, nil
		}
		e := s.entries[i]
		offsetInEntry := cacheOff + int64(n) - e.cacheStart
		toCopy := min(int64(len(b)-n), e.size-offsetInEntry)
		src, err := s.memfd.Slice(e.srcStart+offsetInEntry, toCopy)
		if err != nil {
			return n, fmt.Errorf("memfd slice: %w", err)
		}
		copy(b[n:n+int(toCopy)], src)
		n += int(toCopy)
	}

	return n, nil
}

//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// Memfd wraps a memfd received from Firecracker
type Memfd struct {
	fd   int
	size int

	mmapOnce sync.Once
	mmap     []byte
	mmapErr  error
}

// NewFromFd creates a new Memfd wrapper of a memfd object (fd) that
// backs memory of size bytes big
func NewFromFd(fd, size int) *Memfd {
	return &Memfd{
		fd:   fd,
		size: size,
	}
}

// ensureMapped lazily mmaps the whole memfd. Safe to call from multiple
// goroutines; the mapping is performed exactly once.
func (m *Memfd) ensureMapped() error {
	m.mmapOnce.Do(func() {
		mm, err := syscall.Mmap(m.fd, 0, m.size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			m.mmapErr = fmt.Errorf("failed to mmap memfd: %w", err)

			return
		}
		m.mmap = mm
	})

	return m.mmapErr
}

// Slice returns a zero-copy view of [offset, offset+size) of the memfd.
// The returned slice is valid until Close is called.
func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if err := m.ensureMapped(); err != nil {
		return nil, err
	}
	if offset < 0 || offset >= int64(m.size) || offset+size > int64(m.size) {
		return nil, fmt.Errorf("range [%d, %d) out of bounds (size %d)", offset, offset+size, m.size)
	}

	return m.mmap[offset : offset+size], nil
}

// Close unmaps memory if it was previously mmap'ed and closes the memfd file descriptor
// if not already closed.
func (m *Memfd) Close() error {
	var err error

	if m.mmap != nil {
		if e := syscall.Munmap(m.mmap); e != nil {
			err = fmt.Errorf("munmap memfd: %w", e)
		}
		m.mmap = nil
	}

	if m.fd >= 0 {
		if e := syscall.Close(m.fd); e != nil {
			err = errors.Join(err, fmt.Errorf("close memfd: %w", e))
		}
		m.fd = -1
	}

	return err
}

// MemfdCache wraps a *Cache that is being populated from a memfd.
type MemfdCache struct {
	cache *Cache
	memfd *Memfd
}

func NewCacheFromMemfd(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	ranges []Range,
) (*MemfdCache, error) {
	size := GetSize(ranges)

	cache, err := NewCache(size, blockSize, filePath, false)
	if err != nil {
		if closeErr := memfd.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}

		return nil, err
	}

	if size == 0 {
		// We can close Memfd. We won't be reading anything out of it.
		if closeErr := memfd.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close memfd: %w", closeErr), cache.Close())
		}

		return &MemfdCache{cache: cache}, nil
	}

	memfdCache := &MemfdCache{
		cache: cache,
		memfd: memfd,
	}

	err = memfdCache.writeToDisk(ctx, ranges)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("could not write memfd to disk: %w", err), memfdCache.Close())
	}

	// Close memfd to release the memory
	// At the moment, we always close it. In the future, we will implement
	// copying at the background, so the file descriptor will be kept valid
	if err := memfdCache.memfd.Close(); err != nil {
		logger.L().Warn(ctx, "Could not close memfd", zap.Error(err))
	}
	memfdCache.memfd = nil

	return memfdCache, nil
}

func (m *MemfdCache) writeToDisk(ctx context.Context, ranges []Range) error {
	var cacheOff int64

	for _, r := range ranges {
		rangeStart := cacheOff

		src, err := m.memfd.Slice(r.Start, r.Size)
		if err != nil {
			return fmt.Errorf("bad memfd slice [%d,%d): %w", r.Start, r.Start+r.Size, err)
		}

		for srcOff := int64(0); srcOff < r.Size; srcOff += m.cache.blockSize {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			n := min(m.cache.blockSize, r.Size-srcOff)
			copy((*m.cache.mmap)[cacheOff:cacheOff+n], src[srcOff:srcOff+n])
			cacheOff += n
		}

		m.cache.setIsCached(rangeStart, r.Size)
	}

	return nil
}

func (m *MemfdCache) ReadAt(b []byte, off int64) (int, error) {
	return m.cache.ReadAt(b, off)
}

func (m *MemfdCache) Slice(off, length int64) ([]byte, error) {
	return m.cache.Slice(off, length)
}

func (m *MemfdCache) Size() (int64, error) {
	return m.cache.Size()
}

func (m *MemfdCache) FileSize() (int64, error) {
	return m.cache.FileSize()
}

func (m *MemfdCache) BlockSize() int64 {
	return m.cache.BlockSize()
}

func (m *MemfdCache) Path() string {
	return m.cache.Path()
}

func (m *MemfdCache) Close() error {
	var err error

	if m.memfd != nil {
		if e := m.memfd.Close(); e != nil {
			err = fmt.Errorf("error closing memfd: %w", e)
		}
	}

	if e := m.cache.Close(); e != nil {
		err = errors.Join(err, fmt.Errorf("error closing cache: %w", e))
	}

	return err
}

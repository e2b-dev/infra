//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"syscall"
)

// Memfd wraps a memfd received from Firecracker.
type Memfd struct {
	fd   int
	size int

	mmapOnce sync.Once
	mmap     []byte
	mmapErr  error
}

func NewFromFd(fd, size int) *Memfd {
	return &Memfd{fd: fd, size: size}
}

func (m *Memfd) ensureMapped() error {
	m.mmapOnce.Do(func() {
		mm, err := syscall.Mmap(m.fd, 0, m.size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			m.mmapErr = fmt.Errorf("mmap memfd: %w", err)

			return
		}
		m.mmap = mm
	})

	return m.mmapErr
}

// Slice returns a zero-copy view of [offset, offset+size). Valid until Close.
func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if err := m.ensureMapped(); err != nil {
		return nil, err
	}
	if offset < 0 || offset+size > int64(m.size) {
		return nil, fmt.Errorf("range [%d, %d) out of bounds (size %d)", offset, offset+size, m.size)
	}

	return m.mmap[offset : offset+size], nil
}

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

// memfdCopyChunkSize matches the source hugepage size.
const memfdCopyChunkSize int64 = 2 * 1024 * 1024

// MemfdCache wraps a Cache populated from a memfd.
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
	cache, err := NewCache(GetSize(ranges), blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}

	m := &MemfdCache{cache: cache, memfd: memfd}
	if err := m.copyFromMemfd(ctx, ranges); err != nil {
		return nil, errors.Join(fmt.Errorf("copy from memfd: %w", err), m.Close())
	}
	if err := memfd.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close memfd: %w", err), m.cache.Close())
	}
	m.memfd = nil

	return m, nil
}

// copyFromMemfd is the seam the upcoming async-copy and dedup PRs replace.
func (m *MemfdCache) copyFromMemfd(ctx context.Context, ranges []Range) error {
	var cacheOff int64
	for _, r := range ranges {
		rangeStart := cacheOff

		src, err := m.memfd.Slice(r.Start, r.Size)
		if err != nil {
			return fmt.Errorf("memfd slice [%d,%d): %w", r.Start, r.Start+r.Size, err)
		}

		for srcOff := int64(0); srcOff < r.Size; srcOff += memfdCopyChunkSize {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			n := min(memfdCopyChunkSize, r.Size-srcOff)
			copy((*m.cache.mmap)[cacheOff:cacheOff+n], src[srcOff:srcOff+n])
			cacheOff += n
		}

		m.cache.setIsCached(rangeStart, r.Size)
	}

	return nil
}

func (m *MemfdCache) ReadAt(b []byte, off int64) (int, error) { return m.cache.ReadAt(b, off) }
func (m *MemfdCache) Slice(off, length int64) ([]byte, error) { return m.cache.Slice(off, length) }
func (m *MemfdCache) Size() (int64, error)                    { return m.cache.Size() }
func (m *MemfdCache) FileSize() (int64, error)                { return m.cache.FileSize() }
func (m *MemfdCache) BlockSize() int64                        { return m.cache.BlockSize() }
func (m *MemfdCache) Path() string                            { return m.cache.Path() }

func (m *MemfdCache) Close() error {
	var err error
	if m.memfd != nil {
		if e := m.memfd.Close(); e != nil {
			err = fmt.Errorf("close memfd: %w", e)
		}
	}
	if e := m.cache.Close(); e != nil {
		err = errors.Join(err, fmt.Errorf("close cache: %w", e))
	}

	return err
}

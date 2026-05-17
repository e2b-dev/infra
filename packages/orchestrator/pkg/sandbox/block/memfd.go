//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"syscall"
)

// Memfd wraps a memfd received from Firecracker.
type Memfd struct {
	fd   int
	size int
	mmap []byte
}

func NewFromFd(fd, size int) *Memfd {
	return &Memfd{fd: fd, size: size}
}

// Slice returns a zero-copy view of [offset, offset+size). Valid until Close.
// The underlying mmap is created lazily on first call.
func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if m.mmap == nil {
		b, err := syscall.Mmap(m.fd, 0, m.size, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			return nil, fmt.Errorf("mmap memfd: %w", err)
		}
		m.mmap = b
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

// MemfdCache is a Cache populated from a memfd. The memfd is consumed (and
// closed) during construction; the wrapper exists so the upcoming async-copy
// and dedup PRs can attach extra state without churning callers.
type MemfdCache struct {
	*Cache
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
	if err := copyFromMemfd(ctx, cache, memfd, ranges); err != nil {
		return nil, errors.Join(fmt.Errorf("copy from memfd: %w", err), memfd.Close(), cache.Close())
	}
	if err := memfd.Close(); err != nil {
		return nil, errors.Join(fmt.Errorf("close memfd: %w", err), cache.Close())
	}

	return &MemfdCache{Cache: cache}, nil
}

// copyFromMemfd is the seam the upcoming async-copy and dedup PRs replace.
func copyFromMemfd(ctx context.Context, cache *Cache, memfd *Memfd, ranges []Range) error {
	var cacheOff int64
	for _, r := range ranges {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
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

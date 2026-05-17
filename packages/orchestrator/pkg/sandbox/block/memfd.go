//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"

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

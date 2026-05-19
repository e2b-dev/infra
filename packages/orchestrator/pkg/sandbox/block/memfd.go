//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// Memfd wraps a memfd received from Firecracker. NewFromFd takes ownership.
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

func (m *Memfd) Slice(offset, size int64) ([]byte, error) {
	if offset < 0 || offset+size > int64(len(m.mmap)) {
		return nil, fmt.Errorf("range [%d, %d) out of bounds (size %d)", offset, offset+size, len(m.mmap))
	}

	return m.mmap[offset : offset+size], nil
}

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

func writeAll(fd int, off int64, buff []byte) error {
	remaining := len(buff)
	buffOff := 0

	for remaining > 0 {
		n, err := unix.Pwrite(fd, buff[buffOff:], off)
		if errors.Is(err, unix.EINTR) {
			continue
		}

		if err != nil {
			return err
		}

		if n == 0 {
			return fmt.Errorf("pwrite: EOF with %d bytes remaining", remaining)
		}

		remaining -= n
		buffOff += n
		off += int64(n)
	}

	return nil
}

func NewCacheFromMemfdDeduped(
	ctx context.Context,
	base ReadonlyDevice,
	blockSize int64,
	outPath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*Cache, *header.DiffMetadata, error) {
	src := func(absOff int64) ([]byte, error) { return memfd.Slice(absOff, blockSize) }

	cache, meta, err := dedupPages(ctx, src, base, dirty, blockSize, outPath)
	if err != nil {
		return nil, nil, errors.Join(err, memfd.Close())
	}
	if err := memfd.Close(); err != nil {
		logger.L().Warn(ctx, "close memfd after dedup", zap.Error(err))
	}

	return cache, meta, nil
}

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

//go:build linux

package block

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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

func dedupRange(
	ctx context.Context,
	originalMemfile ReadonlyDevice,
	f *os.File,
	off int64,
	blockSize int64,
	pageDirty, pageEmpty *roaring.Bitmap,
	memfd *Memfd,
	r *Range,
	buff []byte,
) (int64, error) {
	for chunkOff := int64(0); chunkOff < r.Size; chunkOff += blockSize {
		select {
		case <-ctx.Done():
			return off, ctx.Err()
		default:
		}

		srcBuf, err := memfd.Slice(r.Start+chunkOff, blockSize)
		if err != nil {
			return off, err
		}

		_, err = originalMemfile.ReadAt(ctx, buff, r.Start+chunkOff)
		if err != nil {
			return off, fmt.Errorf("failed to read original memfile at offset %d: %w", r.Start+chunkOff, err)
		}

		for i := int64(0); i < blockSize; i += header.PageSize {
			srcPage := srcBuf[i : i+header.PageSize]
			pageIdx := uint32((r.Start + chunkOff + i) / header.PageSize)

			if bytes.Equal(srcPage, buff[i:i+header.PageSize]) {
				if header.IsZero(srcPage) {
					pageEmpty.Add(pageIdx)
				}

				continue
			}

			pageDirty.Add(pageIdx)
			if err = writeAll(int(f.Fd()), off, srcPage); err != nil {
				return off, err
			}

			off += header.PageSize
		}
	}

	return off, nil
}

func NewCacheFromMemfdDeduped(
	ctx context.Context,
	originalMemfile ReadonlyDevice,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*Cache, *header.DiffMetadata, error) {
	ctx, span := tracer.Start(ctx, "new-cache-from-memfd-deduped")
	defer span.End()

	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, nil, errors.Join(fmt.Errorf("error opening cache file: %w", err), memfd.Close())
	}

	var fileOff, exportedSize int64
	var numRanges int
	pageDirty := roaring.NewBitmap()
	pageEmpty := roaring.NewBitmap()
	buff := make([]byte, blockSize)
	for r := range BitsetRanges(dirty, blockSize) {
		numRanges++
		exportedSize += r.Size
		fileOff, err = dedupRange(ctx, originalMemfile, f, fileOff, blockSize, pageDirty, pageEmpty, memfd, &r, buff)
		if err != nil {
			return nil, nil, errors.Join(err, f.Close(), memfd.Close(), os.Remove(filePath))
		}
	}

	if err = f.Close(); err != nil {
		return nil, nil, errors.Join(err, memfd.Close(), os.Remove(filePath))
	}

	cache, err := NewCache(fileOff, header.PageSize, filePath, false)
	if err != nil {
		return nil, nil, errors.Join(err, memfd.Close(), os.Remove(filePath))
	}
	cache.setIsCached(0, fileOff)

	if err = memfd.Close(); err != nil {
		logger.L().Warn(ctx, "Could not close memfd after dedup", zap.Error(err))
	}

	totalPages := exportedSize / header.PageSize
	uniquePages := int64(pageDirty.GetCardinality())
	dedupedPages := totalPages - uniquePages

	telemetry.SetAttributes(
		ctx,
		attribute.Int64("dedup.total_pages", totalPages),
		attribute.Int64("dedup.deduped_pages", dedupedPages),
		attribute.Int64("dedup.unique_pages", uniquePages),
		attribute.Float64("dedup.ratio", safeDivide(float64(dedupedPages), float64(totalPages))),
	)

	logger.L().Info(ctx, "4KiB page dedup completed (memfd fast-path)",
		zap.Int("ranges", numRanges),
		zap.Int64("total_4k_pages", totalPages),
		zap.Int64("deduped_pages", dedupedPages),
		zap.Int64("unique_pages", uniquePages),
		zap.Int64("exported_size_bytes", exportedSize),
		zap.Int64("dedup_size_bytes", fileOff),
		zap.String("reduction", fmt.Sprintf("%.1f%%", safeDivide(float64(dedupedPages), float64(totalPages))*100)),
	)

	return cache, &header.DiffMetadata{
		Dirty:     pageDirty,
		Empty:     pageEmpty,
		BlockSize: header.PageSize,
	}, nil
}

func safeDivide(a, b float64) float64 {
	if b == 0 {
		return 0
	}

	return a / b
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

//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
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
	ctx, span := tracer.Start(ctx, "export-memory-from-memfd",
		trace.WithAttributes(
			attribute.Bool("async", false),
		),
	)
	defer span.End()

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

// MemfdCache wraps a Cache populated from a memfd on a background
// goroutine. Reads block on the copy via Wait. Once it finishes, runCopy
// closes the memfd (releasing the hugetlb pages — potentially tens of GB)
// and reads delegate to the embedded Cache.
type MemfdCache struct {
	cache *Cache

	cancel context.CancelFunc
	done   chan struct{} // closed by runCopy; happens-before for err
	err    error
}

// NewCacheFromMemfdAsync starts the memfd→cache copy on a goroutine so
// Pause can return as soon as the snapshot file + diff metadata are
// written. The returned wrapper takes ownership of memfd; runCopy closes
// it when the copy completes (or is cancelled via Close).
func NewCacheFromMemfdAsync(
	ctx context.Context,
	blockSize int64,
	filePath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
) (*MemfdCache, error) {
	ctx, span := tracer.Start(ctx, "export-memory-from-memfd",
		trace.WithAttributes(
			attribute.Bool("async", true),
		),
	)
	defer span.End()

	cache, err := NewCache(int64(dirty.GetCardinality())*blockSize, blockSize, filePath, false)
	if err != nil {
		return nil, errors.Join(err, memfd.Close())
	}
	if dirty.IsEmpty() {
		if closeErr := memfd.Close(); closeErr != nil {
			return nil, errors.Join(fmt.Errorf("close memfd: %w", closeErr), cache.Close())
		}

		return &MemfdCache{cache: cache}, nil
	}

	// Detach from the request context so the copy outlives Pause.
	copyCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m := &MemfdCache{
		cache:  cache,
		cancel: cancel,
		done:   make(chan struct{}),
	}

	go m.runCopy(copyCtx, memfd, dirty, blockSize)

	return m, nil
}

func (m *MemfdCache) runCopy(ctx context.Context, memfd *Memfd, dirty *roaring.Bitmap, blockSize int64) {
	err := copyFromMemfd(ctx, m.cache, memfd, dirty, blockSize)
	if closeErr := memfd.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close memfd: %w", closeErr))
	}
	m.err = err
	close(m.done)
}

// Wait blocks until the background copy completes (or ctx is cancelled).
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
	if err := m.Wait(context.Background()); err != nil {
		return 0, err
	}

	return m.cache.ReadAt(b, off)
}

func (m *MemfdCache) Slice(off, length int64) ([]byte, error) {
	if err := m.Wait(context.Background()); err != nil {
		return nil, err
	}

	return m.cache.Slice(off, length)
}

func (m *MemfdCache) Close() error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}

	return m.cache.Close()
}

func (m *MemfdCache) Path(ctx context.Context) (string, error) {
	if err := m.Wait(ctx); err != nil {
		return "", err
	}

	return m.cache.filePath, nil
}

func (m *MemfdCache) FileSize(ctx context.Context) (int64, error) {
	if err := m.Wait(ctx); err != nil {
		return 0, err
	}

	return m.cache.FileSize(ctx)
}

func (m *MemfdCache) BlockSize() int64     { return m.cache.BlockSize() }
func (m *MemfdCache) Size() (int64, error) { return m.cache.Size() }

// NewCacheFromMemfdDeduped deduplicates memfd contents against base; see
// dedupPages. Consumes memfd.
func NewCacheFromMemfdDeduped(
	ctx context.Context,
	base ReadonlyDevice,
	blockSize int64,
	outPath string,
	memfd *Memfd,
	dirty *roaring.Bitmap,
	bestEffort bool,
) (*Cache, *header.DiffMetadata, error) {
	src := func(absOff int64) ([]byte, error) { return memfd.Slice(absOff, blockSize) }

	cache, meta, err := dedupPages(ctx, src, base, dirty, blockSize, outPath, bestEffort)
	if err != nil {
		return nil, nil, errors.Join(err, memfd.Close())
	}
	if err := memfd.Close(); err != nil {
		logger.L().Warn(ctx, "close memfd after dedup", zap.Error(err))
	}

	return cache, meta, nil
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

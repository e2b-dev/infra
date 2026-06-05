//go:build linux

package memory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var cowTracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/memory/cow_overlay")

// CoWOverlay wraps a shared base memfile with a sparse overlay file for
// CoW-modified pages. It provides a read-through view: reads check the
// overlay first (via block.Tracker), and fall back to the shared base
// for unmodified pages.
//
// The overlay file is a sparse file the same size as the base. Only pages
// explicitly imported (from a Firecracker process after CoW faults) consume
// physical storage. The Tracker's Dirty state marks pages present in the
// overlay; NotPresent means fall through to the base.
type CoWOverlay struct {
	base        *SharedMemfile
	overlayPath string
	overlayFile *os.File
	overlayData []byte // mmap MAP_SHARED of overlay sparse file
	tracker     *block.Tracker
	pageSize    int64
	size        int64
}

// NewCoWOverlay creates a CoW overlay wrapping the given shared base memfile.
// The overlay file is created/truncated as a sparse file matching base.Size,
// then mmap'd with MAP_SHARED so writes are durable.
func NewCoWOverlay(base *SharedMemfile, overlayPath string) (*CoWOverlay, error) {
	pageSize := int64(unix.Getpagesize())

	f, err := os.OpenFile(overlayPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open overlay file %s: %w", overlayPath, err)
	}

	if err := os.Truncate(overlayPath, base.Size); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncate overlay file: %w", err)
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(base.Size),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("mmap overlay file: %w", err)
	}

	return &CoWOverlay{
		base:        base,
		overlayPath: overlayPath,
		overlayFile: f,
		overlayData: data,
		tracker:     block.NewTracker(),
		pageSize:    pageSize,
		size:        base.Size,
	}, nil
}

// NewCoWOverlayFromCheckpoint loads a CoWOverlay from a previously-saved L2
// checkpoint directory. The checkpoint consists of {path}.data (sparse overlay
// file) and {path}.bitmap (roaring bitmap serialized).
func NewCoWOverlayFromCheckpoint(base *SharedMemfile, checkpointPath string) (*CoWOverlay, error) {
	start := time.Now()
	ctx := context.Background()
	ctx, span := cowTracer.Start(ctx, "load-checkpoint")
	defer span.End()

	dataPath := checkpointPath + ".data"
	bitmapPath := checkpointPath + ".bitmap"

	// Copy the checkpoint data file to our overlay path.
	src, err := os.Open(dataPath)
	if err != nil {
		return nil, fmt.Errorf("open checkpoint data %s: %w", dataPath, err)
	}
	defer src.Close()

	overlay, err := NewCoWOverlay(base, checkpointPath+".vm")
	if err != nil {
		return nil, err
	}

	if _, err := io.Copy(
		io.NewOffsetWriter(overlay.overlayFile, 0),
		src,
	); err != nil {
		overlay.Close()
		return nil, fmt.Errorf("copy checkpoint data: %w", err)
	}

	// Load the dirty bitmap.
	bitmapData, err := os.ReadFile(bitmapPath)
	if err != nil {
		overlay.Close()
		return nil, fmt.Errorf("read checkpoint bitmap: %w", err)
	}

	dirty := roaring.New()
	if err := dirty.UnmarshalBinary(bitmapData); err != nil {
		overlay.Close()
		return nil, fmt.Errorf("unmarshal checkpoint bitmap: %w", err)
	}

	iter := dirty.Iterator()
	for iter.HasNext() {
		idx := iter.Next()
		overlay.tracker.SetRange(idx, idx+1, block.Dirty)
	}

	span.SetAttributes(attribute.Int64("checkpoint.dirty_pages", int64(dirty.GetCardinality())))

	l2CheckpointLoadDuration.Record(ctx, time.Since(start).Milliseconds())

	return overlay, nil
}

// ReadAt reads len(p) bytes starting at offset off. Each page is checked
// against the Tracker: Dirty pages come from the overlay, NotPresent pages
// come from the shared base.
func (c *CoWOverlay) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("negative offset: %d", off)
	}
	if off >= c.size {
		return 0, io.EOF
	}

	toRead := int64(len(p))
	if off+toRead > c.size {
		toRead = c.size - off
	}

	pageSize := c.pageSize
	pos := off
	end := off + toRead

	for pos < end {
		pageIdx := uint32(pos / pageSize)
		pageStart := int64(pageIdx) * pageSize
		pageEnd := pageStart + pageSize
		if pageEnd > c.size {
			pageEnd = c.size
		}

		chunkStart := pos
		chunkEnd := end
		if chunkEnd > pageEnd {
			chunkEnd = pageEnd
		}

		if c.tracker.Get(pageIdx) == block.Dirty {
			copy(p[chunkStart-off:chunkEnd-off], c.overlayData[chunkStart:chunkEnd])
		} else {
			copy(p[chunkStart-off:chunkEnd-off], c.base.Data[chunkStart:chunkEnd])
		}

		pos = chunkEnd
	}

	return int(toRead), nil
}

// ReadPage returns a slice of the page at the given byte offset, preferring
// the overlay if the page is marked Dirty. Returns nil if pageOff is out of
// bounds or not page-aligned.
func (c *CoWOverlay) ReadPage(pageOff int64) []byte {
	if pageOff < 0 || pageOff >= c.size || pageOff%c.pageSize != 0 {
		return nil
	}
	end := pageOff + c.pageSize
	if end > c.size {
		return nil
	}
	pageIdx := uint32(pageOff / c.pageSize)
	if c.tracker.Get(pageIdx) == block.Dirty {
		return c.overlayData[pageOff:end]
	}
	return c.base.Data[pageOff:end]
}

// ImportDirtyPages reads dirty pages from a Firecracker process (identified by
// pid) at the given guest-physical ranges and writes them into the overlay.
// Each successfully-read page is marked Dirty in the Tracker.
func (c *CoWOverlay) ImportDirtyPages(
	ctx context.Context,
	pid int,
	remoteRanges []block.Range,
	logger logger.Logger,
) error {
	ctx, span := cowTracer.Start(ctx, "import-dirty-pages")
	defer span.End()

	span.SetAttributes(attribute.Int64("import.range_count", int64(len(remoteRanges))))

	pageSize := c.pageSize

	for _, r := range remoteRanges {
		if err := c.importRange(ctx, pid, r, pageSize); err != nil {
			logger.Warn(ctx, "CoW overlay import failed for range",
				zap.Int64("start", r.Start),
				zap.Int64("size", r.Size),
				zap.Error(err),
			)
			continue
		}
	}

	dirtyCount := c.DirtyPageCount()
	span.SetAttributes(attribute.Int64("import.dirty_pages", int64(dirtyCount)))
	cowPagesSaved.Add(ctx, int64(dirtyCount))

	return nil
}

func (c *CoWOverlay) importRange(
	ctx context.Context,
	pid int,
	r block.Range,
	pageSize int64,
) error {
	alignedStart := (r.Start / pageSize) * pageSize
	alignedEnd := ((r.Start + r.Size + pageSize - 1) / pageSize) * pageSize
	if alignedEnd > c.size {
		alignedEnd = c.size
	}
	if alignedStart >= alignedEnd {
		return nil
	}

	for off := alignedStart; off < alignedEnd; off += pageSize {
		remote := []unix.RemoteIovec{
			{Base: uintptr(off), Len: int(pageSize)},
		}
		local := []unix.Iovec{
			{Base: &c.overlayData[off], Len: uint64(pageSize)},
		}

		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}

			n, err := unix.ProcessVMReadv(pid, local, remote, 0)
			if errors.Is(err, unix.EAGAIN) {
				continue
			}
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if errors.Is(err, unix.ENOMEM) {
				// ENOMEM is transient when accessing process_vm_readv
				// during high memory pressure.
				time.Sleep(100*time.Millisecond +
					time.Duration(rand.Intn(100))*time.Millisecond)
				continue
			}
			if err != nil {
				return fmt.Errorf("process_vm_readv at offset %d: %w", off, err)
			}
			if int64(n) != pageSize {
				return fmt.Errorf("short read at offset %d: expected %d, got %d", off, pageSize, n)
			}
			break
		}

		pageIdx := uint32(off / pageSize)
		c.tracker.SetRange(pageIdx, pageIdx+1, block.Dirty)
	}

	return nil
}

// ExportDirty returns the overlay data and a clone of the dirty page bitmap.
func (c *CoWOverlay) ExportDirty() ([]byte, *roaring.Bitmap, error) {
	dirty, _ := c.tracker.Export()
	return c.overlayData, dirty, nil
}

// LoadDirty copies the provided data into the overlay and marks pages as
// Dirty according to the bitmap. Used when restoring an L2 checkpoint where
// the data has been read from a saved checkpoint file.
func (c *CoWOverlay) LoadDirty(data []byte, dirty *roaring.Bitmap) error {
	if int64(len(data)) > c.size {
		return fmt.Errorf("data size %d exceeds overlay size %d", len(data), c.size)
	}

	copy(c.overlayData, data)

	iter := dirty.Iterator()
	for iter.HasNext() {
		idx := iter.Next()
		c.tracker.SetRange(idx, idx+1, block.Dirty)
	}

	return nil
}

// SaveCheckpoint persists the CoW overlay data and dirty bitmap to disk.
// Two files are written: {dstPath}.data (sparse overlay) and {dstPath}.bitmap.
func (c *CoWOverlay) SaveCheckpoint(ctx context.Context, dstPath string) error {
	ctx, span := cowTracer.Start(ctx, "save-checkpoint")
	defer span.End()

	dirty, _ := c.tracker.Export()

	dataPath := dstPath + ".data"
	bitmapPath := dstPath + ".bitmap"

	if err := c.writeSparseDataFile(dataPath, dirty); err != nil {
		return fmt.Errorf("write checkpoint data: %w", err)
	}

	bitmapBytes, err := dirty.ToBytes()
	if err != nil {
		return fmt.Errorf("serialize dirty bitmap: %w", err)
	}
	if err := os.WriteFile(bitmapPath, bitmapBytes, 0o644); err != nil {
		return fmt.Errorf("write checkpoint bitmap: %w", err)
	}

	dirtyPages := dirty.GetCardinality()
	span.SetAttributes(attribute.Int64("checkpoint.saved_pages", int64(dirtyPages)))
	cowPagesSaved.Add(ctx, int64(dirtyPages))

	return nil
}

func (c *CoWOverlay) writeSparseDataFile(path string, dirty *roaring.Bitmap) error {
	dst, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open data file: %w", err)
	}
	defer dst.Close()

	if err := os.Truncate(path, c.size); err != nil {
		return fmt.Errorf("truncate data file: %w", err)
	}

	pageSize := c.pageSize
	iter := dirty.Iterator()
	for iter.HasNext() {
		idx := iter.Next()
		off := int64(idx) * pageSize
		end := off + pageSize
		if end > c.size {
			end = c.size
		}
		if _, err := dst.WriteAt(c.overlayData[off:end], off); err != nil {
			return fmt.Errorf("write dirty page at %d: %w", off, err)
		}
	}

	return nil
}

// HasDirtyPages returns true when at least one page has been modified.
func (c *CoWOverlay) HasDirtyPages() bool {
	dirty, _ := c.tracker.Export()
	return !dirty.IsEmpty()
}

// DirtyPageCount returns the number of CoW'd pages.
func (c *CoWOverlay) DirtyPageCount() uint64 {
	dirty, _ := c.tracker.Export()
	return dirty.GetCardinality()
}

// Size returns the overlay size in bytes.
func (c *CoWOverlay) Size() int64 { return c.size }

// Base returns the underlying shared memfile.
func (c *CoWOverlay) Base() *SharedMemfile { return c.base }

// Tracker returns the underlying dirty/zero page tracker.
func (c *CoWOverlay) Tracker() *block.Tracker { return c.tracker }

// Close releases resources associated with the overlay.
func (c *CoWOverlay) Close() error {
	var errs []error

	if c.overlayData != nil {
		if err := unix.Munmap(c.overlayData); err != nil {
			errs = append(errs, fmt.Errorf("munmap overlay: %w", err))
		}
		c.overlayData = nil
	}
	if c.overlayFile != nil {
		if err := c.overlayFile.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close overlay file: %w", err))
		}
		c.overlayFile = nil
	}

	return errors.Join(errs...)
}

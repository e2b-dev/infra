package block

import (
	"context"
	"fmt"
	"strconv"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// CompressedChunker implements a block chunker for compressed data.
// It uses an in-memory LRU cache for decompressed frames to avoid
// re-decompression when adjacent pages are faulted in.
//
// The storage provider (which may be wrapped with NFS cache) handles
// the file-based caching of compressed frames. This chunker only adds
// the in-memory decompressed frame cache on top.
type CompressedChunker struct {
	storage    storage.FrameGetter
	objectPath string
	frameTable *storage.FrameTable
	frameLRU   *FrameLRU
	fetchGroup singleflight.Group
	size       int64
	metrics    metrics.Metrics
}

// NewCompressedChunker creates a new CompressedChunker.
//
// Parameters:
//   - size: total uncompressed size of the data
//   - s: storage provider to fetch frames from (should be wrapped with local cache)
//   - objectPath: path to the object in storage
//   - frameTable: frame table describing the compressed frames
//   - lruSize: number of decompressed frames to keep in memory (0 for default)
//   - m: metrics collector
func NewCompressedChunker(
	size int64,
	s storage.FrameGetter,
	objectPath string,
	frameTable *storage.FrameTable,
	lruSize int,
	m metrics.Metrics,
) (*CompressedChunker, error) {
	if lruSize <= 0 {
		lruSize = DefaultLRUFrameCount
	}

	frameLRU, err := NewFrameLRU(lruSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create frame LRU: %w", err)
	}

	return &CompressedChunker{
		storage:    s,
		objectPath: objectPath,
		frameTable: frameTable,
		frameLRU:   frameLRU,
		size:       size,
		metrics:    m,
	}, nil
}

// ReadAt reads len(b) bytes from the chunker starting at offset off.
func (c *CompressedChunker) ReadAt(ctx context.Context, b []byte, off int64) (int, error) {
	fmt.Printf("<>/<> CompressedChunker/ReadAt path=%s off=%#x len=%#x\n", c.objectPath, off, len(b))
	slice, err := c.Slice(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to slice at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

// Slice returns a slice of the data at the given offset and length.
func (c *CompressedChunker) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

	// Clamp length to available data
	if off+length > c.size {
		length = c.size - off
	}
	if length <= 0 {
		return []byte{}, nil
	}

	// Fetch frames concurrently, writing directly to result buffer
	result := make([]byte, length)
	var eg errgroup.Group

	copied := 0
	for copied < int(length) {
		currentOff := off + int64(copied)

		frameStarts, frameSize, err := c.frameTable.FrameFor(storage.Range{Start: currentOff, Length: 1})
		if err != nil {
			timer.Failure(ctx, length,
				attribute.String(pullType, pullTypeLocal),
				attribute.String(failureReason, "frame_lookup_failed"))

			return nil, fmt.Errorf("failed to get frame for offset %d: %w", currentOff, err)
		}
		fmt.Printf("<>/<> CompressedChunker/Slice path=%s requested offset=%#x length=%#x => frame U offset=%#x, length=%#x\n", c.objectPath, off, length, frameStarts.U, frameSize.U)

		startInFrame := currentOff - frameStarts.U
		remaining := int(length) - copied
		available := int(frameSize.U) - int(startInFrame)
		toCopy := min(remaining, available)
		resultOff := copied
		copied += toCopy

		eg.Go(func() error {
			// Check LRU cache first
			if frame, ok := c.frameLRU.Get(frameStarts.U); ok {
				fmt.Printf("<>/<> CompressedChunker/Slice path=%s frame=%#x -> LRU hit\n", c.objectPath, frameStarts.U)
				copy(result[resultOff:], frame.data[startInFrame:startInFrame+int64(toCopy)])

				return nil
			}

			fmt.Printf("<>/<> CompressedChunker/Slice path=%s frame=%#x -> LRU miss, fetching\n", c.objectPath, frameStarts.U)
			// Fetch with deduplication - concurrent requests for same frame share one fetch
			key := strconv.FormatInt(frameStarts.U, 10)
			dataI, err, shared := c.fetchGroup.Do(key, func() (any, error) {
				// Double-check LRU after acquiring the fetch slot
				if frame, ok := c.frameLRU.Get(frameStarts.U); ok {
					fmt.Printf("<>/<> CompressedChunker/Slice path=%s frame=%#x -> LRU hit after singleflight\n", c.objectPath, frameStarts.U)

					return frame.data, nil
				}
				fmt.Printf("<>/<> CompressedChunker/Slice path=%s frame=%#x -> fetching from storage\n", c.objectPath, frameStarts.U)

				return c.fetchAndDecompress(ctx, frameStarts.U, frameSize)
			})
			if err != nil {
				return err
			}
			data := dataI.([]byte)
			n := copy(result[resultOff:], data[startInFrame:startInFrame+int64(toCopy)])
			fmt.Printf("<>/<> CompressedChunker/Slice path=%s offset %#x requested, copied %#x from frame %#x (shared=%v)\n", c.objectPath, currentOff, n, frameStarts.U, shared)

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))

		return nil, err
	}

	timer.Success(ctx, length, attribute.String(pullType, pullTypeRemote))

	return result, nil
}

// fetchAndDecompress fetches a compressed frame from storage, decompresses it, and stores in LRU.
func (c *CompressedChunker) fetchAndDecompress(ctx context.Context, frameOffU int64, frameSize storage.FrameSize) ([]byte, error) {
	fetchTimer := c.metrics.RemoteReadsTimerFactory.Begin()

	compressedBuf := make([]byte, frameSize.C)
	_, err := c.storage.GetFrame(ctx, c.objectPath, frameOffU, c.frameTable, false, compressedBuf)
	if err != nil {
		fetchTimer.Failure(ctx, int64(frameSize.C),
			attribute.String(failureReason, failureTypeRemoteRead))

		return nil, fmt.Errorf("failed to fetch frame at %#x: %w", frameOffU, err)
	}
	fetchTimer.Success(ctx, int64(frameSize.C))

	if c.frameTable.CompressionType != storage.CompressionZstd {
		return nil, fmt.Errorf("unsupported compression type: %d", c.frameTable.CompressionType)
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer dec.Close()

	data, err := dec.DecodeAll(compressedBuf, make([]byte, 0, frameSize.U))
	if err != nil {
		return nil, fmt.Errorf("failed to decompress frame at %d: %w", frameOffU, err)
	}

	c.frameLRU.Put(frameOffU, int64(frameSize.U), data)

	return data, nil
}

// Close releases all resources used by the chunker.
func (c *CompressedChunker) Close() error {
	if c.frameLRU != nil {
		c.frameLRU.Purge()
	}

	return nil
}

// FileSize returns 0 (no local file - NFS cache handles file storage).
func (c *CompressedChunker) FileSize() (int64, error) {
	return 0, nil
}

// Size returns the total uncompressed size of the data.
func (c *CompressedChunker) Size() int64 {
	return c.size
}

// LRUStats returns statistics about the frame LRU cache.
func (c *CompressedChunker) LRUStats() (count int, maxCount int) {
	return c.frameLRU.Len(), DefaultLRUFrameCount
}

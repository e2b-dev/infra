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

// CompressLRUChunker implements a block chunker for compressed data.
// It uses an in-memory LRU cache for decompressed frames to avoid
// re-decompression when adjacent pages are faulted in.
//
// The storage provider (which may be wrapped with NFS cache) handles
// the file-based caching of compressed frames. This chunker only adds
// the in-memory decompressed frame cache on top.
type CompressLRUChunker struct {
	storage    storage.FrameGetter
	objectPath string
	frameLRU   *FrameLRU
	fetchGroup singleflight.Group
	virtSize   int64 // uncompressed size - used to cap requests
	metrics    metrics.Metrics
}

// NewCompressLRUChunker creates a new CompressLRUChunker.
//
// Parameters:
//   - virtSize: total uncompressed size of the data (used to cap requests)
//   - s: storage provider to fetch frames from (should be wrapped with local cache)
//   - objectPath: path to the object in storage
//   - lruSize: number of decompressed frames to keep in memory (0 for default)
//   - m: metrics collector
func NewCompressLRUChunker(
	virtSize int64,
	s storage.FrameGetter,
	objectPath string,
	lruSize int,
	m metrics.Metrics,
) (*CompressLRUChunker, error) {
	if lruSize <= 0 {
		lruSize = DefaultLRUFrameCount
	}

	frameLRU, err := NewFrameLRU(lruSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create frame LRU: %w", err)
	}

	return &CompressLRUChunker{
		storage:    s,
		objectPath: objectPath,
		frameLRU:   frameLRU,
		virtSize:   virtSize,
		metrics:    m,
	}, nil
}

// Slice returns a slice of the data at the given offset and length.
// The returned slice references internal LRU data and MUST NOT be modified.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressLRUChunker) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	timer := c.metrics.SlicesTimerFactory.Begin()

	// Clamp length to available data
	if off+length > c.virtSize {
		length = c.virtSize - off
	}
	if length <= 0 {
		return []byte{}, nil
	}

	// Find the frame containing the start offset using the passed frame table subset
	frameStarts, frameSize, err := ft.FrameFor(storage.Range{Start: off, Length: 1})
	if err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, "frame_lookup_failed"))

		return nil, fmt.Errorf("failed to get frame for offset %d: %w", off, err)
	}

	startInFrame := off - frameStarts.U
	endInFrame := startInFrame + length

	// Fast path: entire read fits in one frame (common case for 4KB page faults in 4MB frames)
	if endInFrame <= int64(frameSize.U) {
		data, _, err := c.getOrFetchFrame(ctx, frameStarts.U, frameSize, ft)
		if err != nil {
			timer.Failure(ctx, length,
				attribute.String(pullType, pullTypeRemote),
				attribute.String(failureReason, failureTypeCacheFetch))

			return nil, err
		}

		timer.Success(ctx, length, attribute.String(pullType, pullTypeRemote))
		// Return direct slice - no copy needed
		return data[startInFrame:endInFrame], nil
	}

	// Slow path: read spans multiple frames - must assemble result
	result := make([]byte, length)
	var eg errgroup.Group

	copied := 0
	for copied < int(length) {
		currentOff := off + int64(copied)

		frameStarts, frameSize, err := ft.FrameFor(storage.Range{Start: currentOff, Length: 1})
		if err != nil {
			timer.Failure(ctx, length,
				attribute.String(pullType, pullTypeLocal),
				attribute.String(failureReason, "frame_lookup_failed"))

			return nil, fmt.Errorf("failed to get frame for offset %d: %w", currentOff, err)
		}

		startInFrame := currentOff - frameStarts.U
		remaining := int(length) - copied
		available := int(frameSize.U) - int(startInFrame)
		toCopy := min(remaining, available)
		resultOff := copied
		copied += toCopy

		eg.Go(func() error {
			data, _, err := c.getOrFetchFrame(ctx, frameStarts.U, frameSize, ft)
			if err != nil {
				return err
			}
			copy(result[resultOff:], data[startInFrame:startInFrame+int64(toCopy)])

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

// getOrFetchFrame returns frame data from LRU or fetches it from storage.
// Returns the data, whether it was a cache hit, and any error.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressLRUChunker) getOrFetchFrame(ctx context.Context, frameOffU int64, frameSize storage.FrameSize, ft *storage.FrameTable) ([]byte, bool, error) {
	// Check LRU cache first
	if frame, ok := c.frameLRU.get(frameOffU); ok {
		return frame.data, true, nil
	}

	// Fetch with deduplication - concurrent requests for same frame share one fetch
	key := strconv.FormatInt(frameOffU, 10)
	dataI, err, _ := c.fetchGroup.Do(key, func() (any, error) {
		// Double-check LRU after acquiring the fetch slot
		if frame, ok := c.frameLRU.get(frameOffU); ok {
			return frame.data, nil
		}

		return c.fetchAndDecompress(ctx, frameOffU, frameSize, ft)
	})
	if err != nil {
		return nil, false, err
	}

	return dataI.([]byte), false, nil
}

// fetchAndDecompress fetches a compressed frame from storage, decompresses it, and stores in LRU.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressLRUChunker) fetchAndDecompress(ctx context.Context, frameOffU int64, frameSize storage.FrameSize, ft *storage.FrameTable) ([]byte, error) {
	fetchTimer := c.metrics.RemoteReadsTimerFactory.Begin()

	compressedBuf := make([]byte, frameSize.C)
	_, err := c.storage.GetFrame(ctx, c.objectPath, frameOffU, ft, false, compressedBuf)
	if err != nil {
		fetchTimer.Failure(ctx, int64(frameSize.C),
			attribute.String(failureReason, failureTypeRemoteRead))

		return nil, fmt.Errorf("failed to fetch frame at %#x: %w", frameOffU, err)
	}
	fetchTimer.Success(ctx, int64(frameSize.C))

	if ft.CompressionType != storage.CompressionZstd {
		return nil, fmt.Errorf("unsupported compression type: %d", ft.CompressionType)
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

	c.frameLRU.put(frameOffU, int64(frameSize.U), data)

	return data, nil
}

// Close releases all resources used by the chunker.
func (c *CompressLRUChunker) Close() error {
	if c.frameLRU != nil {
		c.frameLRU.Purge()
	}

	return nil
}

// FileSize returns 0 because this chunker uses only in-memory LRU caching.
// It has no local disk files - storage fetches go through the NFS cache layer.
func (c *CompressLRUChunker) FileSize() (int64, error) {
	return 0, nil
}

// Size returns the total uncompressed size of the data.
func (c *CompressLRUChunker) Size() int64 {
	return c.virtSize
}

// LRUStats returns statistics about the frame LRU cache.
func (c *CompressLRUChunker) LRUStats() (count int, maxCount int) {
	return c.frameLRU.Len(), DefaultLRUFrameCount
}

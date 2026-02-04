package block

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/klauspost/compress/zstd"
	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// CompressMMapLRUChunker is a two-level cache chunker:
//   - Level 1: LRU cache for decompressed frames (in-memory)
//   - Level 2: mmap cache for compressed frames (on-disk, lazily populated)
//
// On Slice:
//  1. Check LRU for decompressed frame → hit = return directly
//  2. Check mmap for compressed frame → hit = decompress → LRU → return
//  3. Fetch compressed from storage → mmap → decompress → LRU → return
//
// Benefits over CompressLRU:
//   - After first fetch, LRU misses read from local mmap (not network)
//   - Compressed frames stay cached locally even after LRU eviction
type CompressMMapLRUChunker struct {
	storage    storage.FrameGetter
	objectPath string

	// Level 1: Decompressed frame LRU (same as CompressLRU)
	frameLRU        *FrameLRU
	decompressGroup singleflight.Group // dedup concurrent decompression requests

	// Level 2: Compressed frame mmap cache
	compressedCache *Cache
	frameCached     sync.Map           // map[int64]struct{} - tracks which frames are in mmap (keyed by compressed offset)
	fetchGroup      singleflight.Group // dedup concurrent fetches to storage

	virtSize int64 // uncompressed size - used to cap requests
	rawSize  int64 // compressed file size - used for mmap sizing
	metrics  metrics.Metrics
}

var _ Chunker = (*CompressMMapLRUChunker)(nil)

// NewCompressMMapLRUChunker creates a new two-level cache chunker.
//
// Parameters:
//   - virtSize: total uncompressed size (used to cap requests)
//   - rawSize: total compressed file size (used to size the mmap)
//   - s: storage provider to fetch compressed frames from
//   - objectPath: path to object in storage
//   - cachePath: path for compressed frame mmap file
//   - lruSize: number of decompressed frames to keep in LRU (0 for default)
//   - m: metrics collector
func NewCompressMMapLRUChunker(
	virtSize, rawSize int64,
	s storage.FrameGetter,
	objectPath string,
	cachePath string,
	lruSize int,
	m metrics.Metrics,
) (*CompressMMapLRUChunker, error) {
	if lruSize <= 0 {
		lruSize = DefaultLRUFrameCount
	}

	// Level 1: Decompressed frame LRU
	frameLRU, err := NewFrameLRU(lruSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create frame LRU: %w", err)
	}

	// Level 2: Compressed frame mmap cache - sized to rawSize (compressed file size)
	compressedCache, err := NewCache(rawSize, storage.MemoryChunkSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create compressed cache: %w", err)
	}

	return &CompressMMapLRUChunker{
		storage:         s,
		objectPath:      objectPath,
		frameLRU:        frameLRU,
		compressedCache: compressedCache,
		virtSize:        virtSize,
		rawSize:         rawSize,
		metrics:         m,
	}, nil
}

// Slice returns a slice of data at the given offset and length.
// The returned slice references internal LRU data and MUST NOT be modified.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressMMapLRUChunker) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
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

	// Fast path: entire read fits in one frame
	if endInFrame <= int64(frameSize.U) {
		data, _, err := c.getOrFetchFrame(ctx, frameStarts, frameSize, ft)
		if err != nil {
			timer.Failure(ctx, length,
				attribute.String(pullType, pullTypeRemote),
				attribute.String(failureReason, failureTypeCacheFetch))

			return nil, err
		}

		timer.Success(ctx, length, attribute.String(pullType, pullTypeRemote))

		return data[startInFrame:endInFrame], nil
	}

	// Slow path: read spans multiple frames
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
			data, _, err := c.getOrFetchFrame(ctx, frameStarts, frameSize, ft)
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

// getOrFetchFrame returns decompressed frame data, checking LRU then mmap then storage.
// Returns: data, wasLRUHit, error
// ft is the frame table subset for the specific mapping being read.
func (c *CompressMMapLRUChunker) getOrFetchFrame(ctx context.Context, frameStarts storage.FrameOffset, frameSize storage.FrameSize, ft *storage.FrameTable) ([]byte, bool, error) {
	// Level 1: Check LRU for decompressed frame
	if frame, ok := c.frameLRU.get(frameStarts.U); ok {
		return frame.data, true, nil
	}

	// Dedup concurrent requests for same frame
	key := strconv.FormatInt(frameStarts.U, 10)
	dataI, err, _ := c.decompressGroup.Do(key, func() (any, error) {
		// Double-check LRU
		if frame, ok := c.frameLRU.get(frameStarts.U); ok {
			return frame.data, nil
		}

		return c.fetchDecompressAndCache(ctx, frameStarts, frameSize, ft)
	})
	if err != nil {
		return nil, false, err
	}

	return dataI.([]byte), false, nil
}

// fetchDecompressAndCache ensures compressed frame is in mmap, decompresses, and stores in LRU.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressMMapLRUChunker) fetchDecompressAndCache(ctx context.Context, frameStarts storage.FrameOffset, frameSize storage.FrameSize, ft *storage.FrameTable) ([]byte, error) {
	// Level 2: Ensure compressed frame is in mmap cache
	compressedData, err := c.ensureCompressedInMmap(ctx, frameStarts, frameSize, ft)
	if err != nil {
		return nil, err
	}

	// Decompress
	if ft.CompressionType != storage.CompressionZstd {
		return nil, fmt.Errorf("unsupported compression type: %d", ft.CompressionType)
	}

	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd reader: %w", err)
	}
	defer dec.Close()

	data, err := dec.DecodeAll(compressedData, make([]byte, 0, frameSize.U))
	if err != nil {
		return nil, fmt.Errorf("failed to decompress frame at %#x: %w", frameStarts.U, err)
	}

	// Store in LRU
	c.frameLRU.put(frameStarts.U, int64(frameSize.U), data)

	return data, nil
}

// ensureCompressedInMmap returns compressed frame data from mmap, fetching from storage if needed.
// ft is the frame table subset for the specific mapping being read.
func (c *CompressMMapLRUChunker) ensureCompressedInMmap(ctx context.Context, frameStarts storage.FrameOffset, frameSize storage.FrameSize, ft *storage.FrameTable) ([]byte, error) {
	// Check if already cached in mmap (fast O(1) lookup)
	if _, cached := c.frameCached.Load(frameStarts.C); cached {
		slice, err := c.compressedCache.Slice(frameStarts.C, int64(frameSize.C))
		if err != nil {
			return nil, fmt.Errorf("failed to slice compressed cache: %w", err)
		}

		return slice, nil
	}

	// Not in mmap - fetch from storage (dedup concurrent fetches)
	key := strconv.FormatInt(frameStarts.C, 10)
	_, err, _ := c.fetchGroup.Do(key, func() (any, error) {
		// Double-check after acquiring fetch slot
		if _, cached := c.frameCached.Load(frameStarts.C); cached {
			return nil, nil
		}

		fetchTimer := c.metrics.RemoteReadsTimerFactory.Begin()

		// Get writable slice in mmap
		buf, unlock, err := c.compressedCache.addressBytes(frameStarts.C, int64(frameSize.C))
		if err != nil {
			fetchTimer.Failure(ctx, int64(frameSize.C),
				attribute.String(failureReason, "mmap_address_failed"))

			return nil, fmt.Errorf("failed to get mmap address: %w", err)
		}
		defer unlock()

		// Fetch compressed frame from storage (decompress=false)
		_, err = c.storage.GetFrame(ctx, c.objectPath, frameStarts.U, ft, false, buf)
		if err != nil {
			fetchTimer.Failure(ctx, int64(frameSize.C),
				attribute.String(failureReason, failureTypeRemoteRead))

			return nil, fmt.Errorf("failed to fetch compressed frame at %#x: %w", frameStarts.C, err)
		}

		fetchTimer.Success(ctx, int64(frameSize.C))

		// Mark frame as cached:
		// - compressedCache.setIsCached: needed for Cache.Slice() to work
		// - frameCached: O(1) lookup for our fast path check
		c.compressedCache.setIsCached(frameStarts.C, int64(frameSize.C))
		c.frameCached.Store(frameStarts.C, struct{}{})

		return nil, nil
	})
	if err != nil {
		return nil, err
	}

	// Now read from mmap
	slice, err := c.compressedCache.Slice(frameStarts.C, int64(frameSize.C))
	if err != nil {
		return nil, fmt.Errorf("failed to slice compressed cache after fetch: %w", err)
	}

	return slice, nil
}

// Close releases all resources.
func (c *CompressMMapLRUChunker) Close() error {
	if c.frameLRU != nil {
		c.frameLRU.Purge()
	}

	if c.compressedCache != nil {
		return c.compressedCache.Close()
	}

	return nil
}

// FileSize returns the on-disk size of the compressed mmap cache file.
func (c *CompressMMapLRUChunker) FileSize() (int64, error) {
	return c.compressedCache.FileSize()
}

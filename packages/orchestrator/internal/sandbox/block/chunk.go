package block

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	compressedAttr = "compressed"

	// decompressFetchTimeout is the maximum time a single frame/chunk fetch may take.
	decompressFetchTimeout = 60 * time.Second

	// defaultMinReadBatchSize is the floor for the read batch size when blockSize
	// is very small (e.g. 4KB rootfs). The actual batch is max(blockSize, minReadBatchSize).
	// This reduces syscall overhead and lock/notify frequency.
	defaultMinReadBatchSize = 16 * 1024 // 16 KB
)

// AssetInfo describes which storage variants exist for a build artifact.
type AssetInfo struct {
	BasePath        string // uncompressed path (e.g., "build-123/memfile")
	Size            int64  // uncompressed size (from either source)
	HasUncompressed bool   // true if the uncompressed object exists in storage
	HasLZ4          bool   // true if a .lz4 compressed variant exists
	HasZstd         bool   // true if a .zstd compressed variant exists

	// Opened FramedFile handles — may be nil if the corresponding asset doesn't exist.
	Uncompressed storage.FramedFile
	LZ4          storage.FramedFile
	Zstd         storage.FramedFile
}

// HasCompressed reports whether a compressed asset matching ft's type exists.
func (a *AssetInfo) HasCompressed(ft *storage.FrameTable) bool {
	if ft == nil {
		return false
	}

	switch ft.CompressionType {
	case storage.CompressionLZ4:
		return a.HasLZ4
	case storage.CompressionZstd:
		return a.HasZstd
	default:
		return false
	}
}

// CompressedFile returns the FramedFile for the compression type in ft, or nil.
func (a *AssetInfo) CompressedFile(ft *storage.FrameTable) storage.FramedFile {
	if ft == nil {
		return nil
	}

	switch ft.CompressionType {
	case storage.CompressionLZ4:
		return a.LZ4
	case storage.CompressionZstd:
		return a.Zstd
	default:
		return nil
	}
}

// Chunker fetches data from storage into a memory-mapped cache file.
//
// A single instance serves both compressed and uncompressed callers for the same
// build artifact. The routing decision is made per-GetBlock call based on the
// FrameTable and asset availability:
//
//   - Compressed (ft != nil AND matching compressed asset exists): fetches
//     compressed frames, decompresses them progressively into mmap via GetFrame.
//   - Uncompressed (ft == nil OR no compressed asset): streams raw bytes from
//     storage via GetFrame (with nil frameTable) into mmap.
//
// Both paths use GetFrame with an onRead callback for progressive delivery.
// Decompressed/fetched bytes end up in the shared mmap cache and are
// available to all subsequent callers regardless of compression mode.
type Chunker struct {
	assets AssetInfo

	cache   *Cache
	metrics metrics.Metrics
	flags   *featureflags.Client

	regions *regionLock
}

var _ Reader = (*Chunker)(nil)

// NewChunker creates a Chunker backed by a new mmap cache at cachePath.
func NewChunker(
	assets AssetInfo,
	blockSize int64,
	cachePath string,
	m metrics.Metrics,
	flags *featureflags.Client,
) (*Chunker, error) {
	cache, err := NewCache(assets.Size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	return &Chunker{
		assets:  assets,
		cache:   cache,
		metrics: m,
		flags:   flags,
		regions: newRegionLock(assets.Size),
	}, nil
}

func (c *Chunker) ReadBlock(ctx context.Context, b []byte, off int64, ft *storage.FrameTable) (int, error) {
	slice, err := c.GetBlock(ctx, off, int64(len(b)), ft)
	if err != nil {
		return 0, fmt.Errorf("failed to get block at %d-%d: %w", off, off+int64(len(b)), err)
	}

	return copy(b, slice), nil
}

// GetBlock returns data at the given uncompressed offset as a reference to the
// mmap cache. On cache miss, fetches from storage (decompressing if ft is
// non-nil and a matching compressed asset exists).
func (c *Chunker) GetBlock(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	// if off < 0 || length < 0 {
	// 	return nil, fmt.Errorf("invalid slice params: off=%d length=%d", off, length)
	// }
	// if off+length > c.assets.Size {
	// 	return nil, fmt.Errorf("slice out of bounds: off=%#x length=%d size=%d", off, length, c.assets.Size)
	// }

	useCompressed := c.assets.HasCompressed(ft)

	timer := c.metrics.SlicesTimerFactory.Begin(
		attribute.Bool(compressedAttr, useCompressed),
	)

	// Fast path: already in mmap cache.
	b, err := c.cache.Slice(off, length)
	if err == nil {
		timer.Success(ctx, length,
			attribute.String(pullType, pullTypeLocal))

		return b, nil
	}

	if !errors.As(err, &BytesNotAvailableError{}) {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalRead))

		return nil, fmt.Errorf("failed read from cache at offset %d: %w", off, err)
	}

	session, sessionErr := c.getOrCreateSession(ctx, off, ft, useCompressed)
	if sessionErr != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, "session_create"))

		return nil, sessionErr
	}

	if err := session.registerAndWait(ctx, off, length); err != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeRemote),
			attribute.String(failureReason, failureTypeCacheFetch))

		return nil, fmt.Errorf("failed to fetch data at %#x: %w", off, err)
	}

	b, cacheErr := c.cache.Slice(off, length)
	if cacheErr != nil {
		timer.Failure(ctx, length,
			attribute.String(pullType, pullTypeLocal),
			attribute.String(failureReason, failureTypeLocalReadAgain))

		return nil, fmt.Errorf("failed to read from cache after fetch at %d-%d: %w", off, off+length, cacheErr)
	}

	timer.Success(ctx, length,
		attribute.String(pullType, pullTypeRemote))

	return b, nil
}

// getOrCreateSession returns an existing session covering [off, off+...) or
// creates a new one. Session boundaries are frame-aligned for compressed
// requests and MemoryChunkSize-aligned for uncompressed requests.
//
// Deduplication and overlap prevention are handled by the regionLock's
// slot-based tracking: each MemoryChunkSize-aligned interval stores the
// active session pointer. Single-slot requests join an existing session;
// multi-slot requests wait for all occupied slots to clear.
func (c *Chunker) getOrCreateSession(ctx context.Context, off int64, ft *storage.FrameTable, useCompressed bool) (*fetchSession, error) {
	var (
		chunkOff   int64
		chunkLen   int64
		decompress bool
	)

	if useCompressed {
		frameStarts, frameSize, err := ft.FrameFor(off)
		if err != nil {
			return nil, fmt.Errorf("failed to get frame for offset %#x: %w", off, err)
		}

		chunkOff = frameStarts.U
		chunkLen = int64(frameSize.U)
		decompress = true
	} else {
		chunkOff = (off / storage.MemoryChunkSize) * storage.MemoryChunkSize
		chunkLen = min(int64(storage.MemoryChunkSize), c.assets.Size-chunkOff)
		decompress = false
	}

	session, isNew := c.regions.getOrCreate(chunkOff, chunkLen, func() *fetchSession {
		return newFetchSession(chunkOff, chunkLen, c.cache.BlockSize(), c.cache.isCached)
	})

	if isNew {
		go c.runFetch(context.WithoutCancel(ctx), session, chunkOff, ft, decompress)
	}

	return session, nil
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
// Works for both compressed (decompress=true, ft!=nil) and uncompressed (decompress=false, ft=nil) paths.
func (c *Chunker) runFetch(ctx context.Context, s *fetchSession, offsetU int64, ft *storage.FrameTable, decompress bool) {
	ctx, cancel := context.WithTimeout(ctx, decompressFetchTimeout)
	defer cancel()

	// Release region slots after session completes. This must run after
	// setDone/setError so that session waiters are notified before slot
	// claimers are unblocked.
	defer c.regions.release(s.chunkOff, s.chunkLen)

	defer func() {
		if r := recover(); r != nil {
			s.setError(fmt.Errorf("fetch panicked: %v", r), true)
		}
	}()

	// Get mmap region for the fetch target.
	mmapSlice, releaseLock, err := c.cache.addressBytes(s.chunkOff, s.chunkLen)
	if err != nil {
		s.setError(err, false)

		return
	}
	defer releaseLock()

	fetchSW := c.metrics.RemoteReadsTimerFactory.Begin(
		attribute.Bool(compressedAttr, decompress),
	)

	// Compute read batch size from FF + block size.
	blockSize := c.cache.BlockSize()
	minBatch := int64(defaultMinReadBatchSize)
	if v := c.flags.JSONFlag(ctx, featureflags.ChunkerConfigFlag).AsValueMap().Get("minReadBatchSizeKB"); v.IsNumber() {
		minBatch = int64(v.IntValue()) * 1024
	}
	readSize := max(blockSize, minBatch)

	// Build onRead callback: publishes blocks to mmap cache and wakes waiters
	// as each readSize-aligned chunk arrives.
	var prevTotal int64
	onRead := func(totalWritten int64) {
		newBytes := totalWritten - prevTotal
		c.cache.setIsCached(s.chunkOff+prevTotal, newBytes)
		s.advance(totalWritten)
		prevTotal = totalWritten
	}

	var handle storage.FramedFile
	if decompress {
		handle = c.assets.CompressedFile(ft)
	} else {
		handle = c.assets.Uncompressed
	}

	_, err = handle.GetFrame(ctx, offsetU, ft, decompress, mmapSlice[:s.chunkLen], readSize, onRead)
	if err != nil {
		fetchSW.Failure(ctx, s.chunkLen,
			attribute.String(failureReason, failureTypeRemoteRead))
		s.setError(fmt.Errorf("failed to fetch data at %#x: %w", offsetU, err), false)

		return
	}

	fetchSW.Success(ctx, s.chunkLen)
	s.setDone()
}

func (c *Chunker) Close() error {
	return c.cache.Close()
}

func (c *Chunker) FileSize() (int64, error) {
	return c.cache.FileSize()
}

const (
	pullType       = "pull-type"
	pullTypeLocal  = "local"
	pullTypeRemote = "remote"

	failureReason = "failure-reason"

	failureTypeLocalRead      = "local-read"
	failureTypeLocalReadAgain = "local-read-again"
	failureTypeRemoteRead     = "remote-read"
	failureTypeCacheFetch     = "cache-fetch"
)

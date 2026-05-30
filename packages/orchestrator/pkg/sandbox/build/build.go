//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	header      atomic.Pointer[header.Header]
	store       *DiffStore
	fileType    DiffType
	persistence storage.StorageProvider
	metrics     blockmetrics.Metrics
}

func NewFile(
	header *header.Header,
	store *DiffStore,
	fileType DiffType,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
) *File {
	f := &File{
		store:       store,
		fileType:    fileType,
		persistence: persistence,
		metrics:     metrics,
	}
	f.header.Store(header)

	return f
}

func (b *File) Header() *header.Header {
	return b.header.Load()
}

func (b *File) SwapHeader(h *header.Header) {
	b.header.Store(h)
}

func (b *File) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	maxParallel := 1
	if b.store != nil && b.store.flags != nil {
		maxParallel = b.store.flags.IntFlag(ctx, featureflags.MaxParallelBuildReadSegments)
	}
	if maxParallel > 1 && len(p) > 0 {
		if err := b.readAtParallel(ctx, p, off, maxParallel); err == nil {
			return len(p), nil
		} else if shouldRetrySerial(err) {
			if retry, swapErr := b.retryOnTransition(ctx, err); !retry && swapErr != nil {
				return 0, swapErr
			}

			return b.readAtSerial(ctx, p, off)
		}

		return 0, err
	}

	return b.readAtSerial(ctx, p, off)
}

func (b *File) readAtSerial(ctx context.Context, p []byte, off int64) (n int, err error) {
	// Cache some resolved Diffs per BuildId for the duration of one ReadAt to avoid
	// hitting the DiffStore TTL cache (and its mutex) on every iteration.
	const buildCacheSize = 16
	var (
		underlyingIDs   [buildCacheSize]uuid.UUID
		underlyingDiffs [buildCacheSize]Diff
		cacheIDs        = underlyingIDs[:0]
		cacheDiffs      = underlyingDiffs[:0]
	)

	for n < len(p) {
		h := b.Header()

		// Find out what build and ranghe we need to read, from mappings.
		mappedToBuild, err := h.GetShiftedMapping(ctx, off+int64(n))
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		remainingReadLength := int64(len(p)) - int64(n)
		readLength := min(int64(mappedToBuild.Length), remainingReadLength)

		if readLength <= 0 {
			logger.L().Error(ctx, fmt.Sprintf(
				"(%d bytes left to read, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d (mapped length: %d, remaining read length: %d)\n>>> EOF\n",
				len(p)-n,
				off,
				readLength,
				mappedToBuild.BuildId,
				b.fileType,
				mappedToBuild.Offset,
				n,
				int64(n)+readLength,
				n,
				mappedToBuild.Length,
				remainingReadLength,
			))

			return n, io.EOF
		}

		if mappedToBuild.BuildId == uuid.Nil {
			clear(p[n : int64(n)+readLength])
			n += int(readLength)

			continue
		}

		size := b.buildFileSize(h, mappedToBuild.BuildId)
		ft := h.GetBuildFrameData(mappedToBuild.BuildId)

		// Find the build in the caches.
		var mappedBuild Diff
		hitIdx := -1
		for i, id := range cacheIDs {
			if id == mappedToBuild.BuildId {
				mappedBuild = cacheDiffs[i]
				hitIdx = i

				break
			}
		}
		if mappedBuild == nil {
			mappedBuild, err = b.getBuild(ctx, mappedToBuild.BuildId, size, ft.CompressionType())
			if err != nil {
				return 0, fmt.Errorf("failed to get build: %w", err)
			}
			if len(cacheIDs) < cap(cacheIDs) {
				cacheIDs = append(cacheIDs, mappedToBuild.BuildId)
				cacheDiffs = append(cacheDiffs, mappedBuild)
				hitIdx = len(cacheIDs) - 1
			}
		}

		// Read from that build, and handle the various retries.
		buildN, err := mappedBuild.ReadAt(ctx,
			p[n:int64(n)+readLength],
			int64(mappedToBuild.Offset),
			ft,
		)
		if err != nil {
			// Cache may have evicted+closed the Diff between resolve and ReadAt;
			// drop the stale entry and re-resolve on the next iteration.
			var closed *block.CacheClosedError
			if errors.As(err, &closed) {
				if hitIdx >= 0 {
					last := len(cacheIDs) - 1
					cacheIDs[hitIdx] = cacheIDs[last]
					cacheDiffs[hitIdx] = cacheDiffs[last]
					cacheIDs = cacheIDs[:last]
					cacheDiffs = cacheDiffs[:last]
				}

				continue
			}
			if retry, swapErr := b.retryOnTransition(ctx, err); retry {
				continue
			} else if swapErr != nil {
				return 0, swapErr
			}

			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += buildN
	}

	return n, nil
}

type readSegment struct {
	dstOff int
	srcOff int64
	length int64
	diff   Diff
	ft     *storage.FrameTable
}

func (b *File) readAtParallel(ctx context.Context, p []byte, off int64, maxParallel int) error {
	segments, err := b.planRead(ctx, p, off)
	if err != nil {
		return err
	}
	if len(segments) <= 1 {
		for _, s := range segments {
			n, err := s.diff.ReadAt(ctx, p[s.dstOff:s.dstOff+int(s.length)], s.srcOff, s.ft)
			if err != nil {
				return err
			}
			if int64(n) != s.length {
				return io.ErrUnexpectedEOF
			}
		}

		return nil
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxParallel)
	for _, s := range segments {
		seg := s
		g.Go(func() error {
			n, err := seg.diff.ReadAt(gctx, p[seg.dstOff:seg.dstOff+int(seg.length)], seg.srcOff, seg.ft)
			if err != nil {
				return err
			}
			if int64(n) != seg.length {
				return io.ErrUnexpectedEOF
			}

			return nil
		})
	}

	return g.Wait()
}

func (b *File) planRead(ctx context.Context, p []byte, off int64) ([]readSegment, error) {
	const buildCacheSize = 16
	var (
		underlyingIDs   [buildCacheSize]uuid.UUID
		underlyingDiffs [buildCacheSize]Diff
		cacheIDs        = underlyingIDs[:0]
		cacheDiffs      = underlyingDiffs[:0]
		segments        []readSegment
	)

	var n int
	for n < len(p) {
		h := b.Header()
		mappedToBuild, err := h.GetShiftedMapping(ctx, off+int64(n))
		if err != nil {
			return nil, fmt.Errorf("failed to get mapping: %w", err)
		}
		readLength := min(int64(mappedToBuild.Length), int64(len(p)-n))
		if readLength <= 0 {
			return nil, io.EOF
		}
		if mappedToBuild.BuildId == uuid.Nil {
			clear(p[n : n+int(readLength)])
			n += int(readLength)

			continue
		}

		diff, err := b.cachedBuild(ctx, h, mappedToBuild.BuildId, &cacheIDs, &cacheDiffs)
		if err != nil {
			return nil, err
		}
		segments = append(segments, readSegment{
			dstOff: n,
			srcOff: int64(mappedToBuild.Offset),
			length: readLength,
			diff:   diff,
			ft:     h.GetBuildFrameData(mappedToBuild.BuildId),
		})
		n += int(readLength)
	}

	return segments, nil
}

func (b *File) cachedBuild(ctx context.Context, h *header.Header, buildID uuid.UUID, ids *[]uuid.UUID, diffs *[]Diff) (Diff, error) {
	for i, id := range *ids {
		if id == buildID {
			return (*diffs)[i], nil
		}
	}

	diff, err := b.getBuild(ctx, buildID, b.buildFileSize(h, buildID), h.GetBuildFrameData(buildID).CompressionType())
	if err != nil {
		return nil, fmt.Errorf("failed to get build: %w", err)
	}
	if len(*ids) < cap(*ids) {
		*ids = append(*ids, buildID)
		*diffs = append(*diffs, diff)
	}

	return diff, nil
}

func shouldRetrySerial(err error) bool {
	var closed *block.CacheClosedError
	if errors.As(err, &closed) {
		return true
	}
	var transErr *storage.PeerTransitionedError

	return errors.As(err, &transErr)
}

// Slice returns [off, off+length). Zero-copy when the range fits in a
// single mapping; otherwise composes via ReadAt.
func (b *File) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	if length > 0 {
		h := b.Header()
		m, err := h.GetShiftedMapping(ctx, off)
		if err == nil && int64(m.Length) >= length {
			if m.BuildId == uuid.Nil && length <= int64(len(header.EmptyHugePage)) {
				return header.EmptyHugePage[:length], nil
			}
			if m.BuildId != uuid.Nil {
				size := b.buildFileSize(h, m.BuildId)
				ft := h.GetBuildFrameData(m.BuildId)
				diff, derr := b.getBuild(ctx, m.BuildId, size, ft.CompressionType())
				if derr != nil {
					logger.L().Warn(ctx, "failed to get build for slice fast path", zap.Error(derr))
				} else {
					slice, sErr := diff.Slice(ctx, int64(m.Offset), length, ft)
					if sErr == nil {
						return slice, nil
					}
					logger.L().Warn(ctx, "failed to slice build fast path", zap.Error(sErr))
				}
				// Errors fall through to ReadAt's retry-on-transition path.
			}
		}
	}
	out := make([]byte, length)
	if _, err := b.ReadAt(ctx, out, off); err != nil {
		return nil, fmt.Errorf("failed to read at: %w", err)
	}

	return out, nil
}

// IsCached reports whether the range is fully resident locally; uuid.Nil
// counts as cached, uninitialized StorageDiffs as uncached. No I/O.
func (b *File) IsCached(ctx context.Context, off, length int64) bool {
	h := b.Header()
	if h == nil {
		return false
	}

	var n int64
	for n < length {
		m, err := h.GetShiftedMapping(ctx, off+n)
		if err != nil {
			return false
		}
		segLen := min(int64(m.Length), length-n)
		if segLen <= 0 {
			return false
		}

		if m.BuildId != uuid.Nil {
			diff, ok := b.store.Lookup(GetDiffStoreKey(m.BuildId.String(), b.fileType))
			if !ok {
				return false
			}
			peeker, ok := diff.(block.CachePeeker)
			if !ok || !peeker.IsCached(ctx, int64(m.Offset), segLen) {
				return false
			}
		}

		n += segLen
	}

	return true
}

// retryOnTransition catches a PeerTransitionedError and swaps the header from
// storage. Returns (true, nil) to signal the caller should continue the loop,
// or (false, swapErr) if the swap itself failed. RetryAfter backs off repeated
// post-transition storage 404s.
//
// The transition is signaled only after the source upload has finalized, so
// the header object already exists in storage. A single LoadHeader is enough;
// polling here would multiply GCS reads under high peer-transition rates.
func (b *File) retryOnTransition(ctx context.Context, err error) (bool, error) {
	var transErr *storage.PeerTransitionedError
	if !errors.As(err, &transErr) {
		return false, nil
	}
	if transErr.RetryAfter > 0 {
		timer := time.NewTimer(transErr.RetryAfter)
		defer timer.Stop()

		select {
		case <-timer.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	logger.L().Info(ctx, "peer transition detected, swapping header",
		zap.String("file_type", string(b.fileType)),
	)

	hdrPath := storage.Paths{BuildID: b.Header().Metadata.BuildId.String()}.HeaderFile(string(b.fileType))
	h, loadErr := header.LoadHeader(ctx, b.persistence, hdrPath)
	if loadErr != nil {
		return false, fmt.Errorf("failed to swap header: %w", loadErr)
	}
	b.SwapHeader(h)

	return true, nil
}

// buildFileSize returns the uncompressed file size for a build. Returns 0 for
// V3 headers, which signals the read path to fall back to a Size() RPC.
func (b *File) buildFileSize(h *header.Header, buildID uuid.UUID) int64 {
	if bd, ok := h.Builds[buildID]; ok {
		return bd.Size
	}

	return 0
}

func (b *File) getBuild(ctx context.Context, buildID uuid.UUID, uncompressedSize int64, ct storage.CompressionType) (Diff, error) {
	storageDiff, err := newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		int64(b.Header().Metadata.BlockSize),
		b.metrics,
		b.persistence,
		uncompressedSize, ct,
		b.store.flags,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage diff: %w", err)
	}

	source, err := b.store.Get(ctx, storageDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to get build from store: %w", err)
	}

	return source, nil
}
